// Package flapper implements an interface for controlling a set of splitflap
// displays, built using the design at github.com/scottbez1/splitflap. Commands
// are sent over a tty connection to the controller board, using the new
// protobuf interface to the controller.
package flapper

//go:generate bash script/gen-proto.sh

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/dgryski/go-cobs"
	"github.com/muesli/reflow/padding"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/trapgate/flapper/proto"
	"go.bug.st/serial"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	gproto "google.golang.org/protobuf/proto"
)

const (
	retryTimeout = 250 * time.Millisecond

	// TODO: Get this from the display
	runeSet = " abcdefghijklmnopqrstuvwxyz0123456789.,'"
)

type sendReq struct {
	msg *proto.ToSplitflap
	ch  chan<- error
}

// Display represents one or more splitflap units connected to a controller.
type Display struct {
	dev       string      // The tty device used to talk to the display
	nonce     uint32      // nonce is incremented every time we send a pb
	port      serial.Port // The serial device.
	rw        io.ReadWriteCloser
	toDisplay chan sendReq

	text       string               // The text being displayed
	cells      int                  // The number of units in the display
	lastStatus proto.SplitflapState // The most recent status report from the display.
	runes      map[rune]int
}

// NewDisplay returns a new Display struct, representing a splitflap display
// with one or more modules.
func NewDisplay() (*Display, error) {
	d := &Display{
		// This is the device used for the TTGO.
		dev:        "/dev/ttyACM0",
		nonce:      rand.Uint32(),
		toDisplay:  make(chan sendReq),
		cells:      24, // TODO: Get this from the display
		lastStatus: proto.SplitflapState{Settings: &proto.Settings{}},
		runes:      make(map[rune]int),
	}

	fmt.Println("connecting to display")
	err := d.connect()
	if err != nil {
		return nil, err
	}

	// Start the goroutine that will read frames send back from the display
	fmt.Println("starting display goroutine")
	go d.communicate(d.toDisplay)

	// TODO: Wait for the result.
	d.readStatus()
	for i, r := range runeSet {
		d.runes[r] = i
	}

	return d, err
}

func (d *Display) connect() error {
	// The Arduino used 38400; the baud rate of the TTGO TDisplay is 230400.
	mode := &serial.Mode{BaudRate: 230400}
	p, err := serial.Open(d.dev, mode)
	if err != nil {
		return err
	}
	d.port = p
	d.rw = p

	return err
}

// Close will close the serial port and stop the comms goroutine.
func (d *Display) Close() {
	d.rw.Close()
	close(d.toDisplay)
}

// HardReset will reset the whole microcontroller.
func (d *Display) HardReset() {
	d.port.SetRTS(true)
	d.port.SetDTR(false)
	time.Sleep(200 * time.Millisecond)
	d.port.SetDTR(true)
	time.Sleep(200 * time.Millisecond)
}

// readFrames will read bytes from the serial port, assemble them into a frame,
// decode it, and send the resulting protobuf message to the fromDisplay
// channel. This should be run in a goroutine.
// TODO: Handle shutdown cleanly.
func (d *Display) readFrames(fromDisplay chan<- *proto.FromSplitflap) {
	rdr := bufio.NewReader(d.rw)

	for {
		b, err := rdr.ReadBytes(0)
		if err != nil {
			panic("failed to read from display")
		}

		msg, err := decodeMsg(b)
		if err != nil {
			fmt.Println(err)
			continue
		}

		// send the decode message to anyone who might be listening.
		fromDisplay <- msg
	}
}

func decodeMsg(b []byte) (*proto.FromSplitflap, error) {
	// Frames include a 4-byte crc and a terminating null, or else discard them.
	if len(b) < 5 {
		// Empty frame. Just keep trying.
		return nil, errors.New("empty frame")
	}
	// decode the buffer. Don't include the zero byte.
	b, err := cobs.Decode(b[:len(b)-1])
	if err != nil {
		return nil, errors.New("failed to decode cobs frame")
	}

	crcBytes := b[len(b)-4:]
	b = b[:len(b)-4]
	crc := binary.LittleEndian.Uint32(crcBytes)
	if crc32.ChecksumIEEE(b) != crc {
		return nil, errors.New("bad crc")
	}

	msg := &proto.FromSplitflap{}
	err = gproto.Unmarshal(b, msg)
	if err != nil {
		return nil, errors.New("failed to unmarshal protobuf message")
	}
	return msg, nil
}

// write will send a protobuf message to the splitflap display.
func (d *Display) write(msg *proto.ToSplitflap) error {
	b, err := encodeMsg(msg)
	if err != nil {
		return err
	}
	_, err = d.rw.Write(b)
	if err != nil {
		return err
	}

	return nil
}

func encodeMsg(msg *proto.ToSplitflap) ([]byte, error) {
	b, err := gproto.Marshal(msg)
	if err != nil {
		return nil, err
	}

	// Append the crc32 value to the end of the payload before sending it.
	crc := crc32.ChecksumIEEE(b)
	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)
	b = append(b, crcBytes...)

	// cobs is used to encode the buffer with no zero bytes.
	cb := append(cobs.Encode(b), byte(0))
	return cb, nil
}

func (d *Display) communicate(toDisplay <-chan sendReq) {
	fromDisplay := make(chan *proto.FromSplitflap)
	acks := make(chan uint32)

	// Incoming frames from the display are read by this goroutine.
	go d.readFrames(fromDisplay)
	go d.writeMsgs(toDisplay, acks)

	for msg := range fromDisplay {
		d.handleFromMsg(msg, acks)
		// TODO: Send this message to anyone who has registered for it.
		// TODO: Handle shutdown
	}
}

func (d *Display) nextNonce() uint32 {
	n := d.nonce % 255
	d.nonce++
	return n
}

func (d *Display) writeMsgs(toDisplay <-chan sendReq, acks <-chan uint32) {
	rand.Seed(time.Now().UnixMicro())
	nonce := d.nextNonce()

	for req := range toDisplay {
		req.msg.Nonce = nonce

		for acked := false; !acked; {
			fmt.Println("sending", req.msg)
			err := d.write(req.msg)
			if err != nil {
				req.ch <- err
			}

			timer := time.NewTimer(retryTimeout)
			select {
			case ackNonce := <-acks:
				if ackNonce == nonce {
					acked = true
					req.ch <- nil
					if !timer.Stop() {
						<-timer.C
					}
				}
			case <-timer.C:
				fmt.Println("send timed out; resending")
				// Nothing to do here; we'll loop and retry
			}
		}

		nonce = d.nextNonce()
	}
}

func (d *Display) handleFromMsg(msg *proto.FromSplitflap, acks chan<- uint32) {
	switch msg.Payload.(type) {
	case *proto.FromSplitflap_SplitflapState:
		d.lastStatus = *msg.GetSplitflapState()
		d.cells = len(d.lastStatus.Modules)
		d.text = currentText(&d.lastStatus)
		// dumpStateMsg(&d.lastStatus)
	case *proto.FromSplitflap_Log:
		// For now just print them.
		fmt.Println(msg.GetLog().Msg)
	case *proto.FromSplitflap_Ack:
		fmt.Printf("received ack for %v\n", msg.GetAck().GetNonce())
		acks <- msg.GetAck().GetNonce()
	default:
		fmt.Println("received", msg)
	}
}

func currentText(msg *proto.SplitflapState) string {
	text := strings.Builder{}
	for _, m := range msg.Modules {
		text.WriteByte(runeSet[m.FlapIndex])
	}
	return text.String()
}

// dumpStateMsg displays a SplitflapState message to the terminal, using color.
func dumpStateMsg(msg *proto.SplitflapState) {
	// Settings first
	off := lipgloss.NewStyle().Foreground(lipgloss.Color("#C0C0C0"))
	on := lipgloss.NewStyle().Foreground(lipgloss.Color("#10D000"))
	style := &off
	if msg.Settings.ForceFullRotation {
		style = &on
	}
	fmt.Printf("%v maxmoving: %v startdelay: %v animstyle: %v\n",
		style.Render("fullrotation"),
		msg.Settings.MaxMoving,
		msg.Settings.StartDelayMillis,
		proto.Settings_AnimationStyle_name[int32(msg.Settings.AnimationStyle)],
	)

	// Now show each module
	moving := lipgloss.NewStyle().Foreground(lipgloss.Color("#F0D000"))
	stopped := lipgloss.NewStyle().Foreground(lipgloss.Color("#1030E0"))
	normal := lipgloss.NewStyle().Foreground(lipgloss.Color("#00E010"))
	er := lipgloss.NewStyle().Foreground(lipgloss.Color("#F00010"))

	for i, m := range msg.Modules {
		if i == 12 {
			fmt.Println()
		}
		style := &stopped
		char := runeSet[m.FlapIndex]
		if m.Moving {
			style = &moving
		}
		fmt.Print(style.Render(string(char)))
	}
	fmt.Println()
	for i, m := range msg.Modules {
		style := &normal
		if m.State != proto.SplitflapState_ModuleState_NORMAL {
			style = &er
		}
		state := proto.SplitflapState_ModuleState_State_name[int32(m.State)]
		if m.State == 0 {
			state = "N"
		}
		fmt.Print(
			style.Render(
				fmt.Sprintf("%v@%v: %v %d/%d ", i, m.FlapIndex,
					state,
					m.CountMissedHome, m.CountUnexpectedHome)))
	}
	fmt.Println()
}

// Init requests the display state, and should be called after connecting.
func (d *Display) Init() error {
	fmt.Println("init display")
	ch := make(chan error)
	req := sendReq{
		msg: &proto.ToSplitflap{
			Payload: &proto.ToSplitflap_RequestState{
				RequestState: &proto.RequestState{},
			},
		},
		ch: ch,
	}
	d.toDisplay <- req
	return <-ch
}

// SetText will display the passed string on the splitflaps. If the string is
// shorter than the available cells on the display it will be padded with
// spaces; if it's longer it will be truncated mercilessly.
// TODO: validate each character - don't pass runes the display can't display.
func (d *Display) SetText(text string) error {
	text = d.PrepText(text)
	ch := make(chan error)

	fmt.Println(text)
	mc := make([]*proto.SplitflapCommand_ModuleCommand, d.cells)
	for i, r := range text {
		mc[i] = &proto.SplitflapCommand_ModuleCommand{
			Action: proto.SplitflapCommand_ModuleCommand_GO_TO_FLAP,
			Param:  uint32(d.runes[r]),
		}
	}
	req := sendReq{
		msg: &proto.ToSplitflap{
			Payload: &proto.ToSplitflap_SplitflapCommand{
				SplitflapCommand: &proto.SplitflapCommand{
					Modules: mc,
				},
			},
		},
		ch: ch,
	}

	d.toDisplay <- req

	return <-ch
}

func (d *Display) PrepText(text string) string {
	// First, normalize the text so that it only has characters the display can
	// show.
	text = d.normalize(text)
	// This works imperfectly. It does accomplish forcing a line break when the
	// top word is longer than 12 runes, but when it does so the second line
	// doesn't get appended to the broken first. Also, it's not possible to
	// indent line 2, because the word wrap consumes all the spaces.
	text = wrap.String(wordwrap.String(text, 12), 12)
	lines := strings.SplitN(text, "\n", 3)
	// fmt.Printf("%q %q\n", lines[0], lines[1])
	if len(lines) < 2 {
		lines = append(lines, " ")
	}
	for i, line := range lines[:2] {
		// If leading spaces are used to push the word to line 2, line 1 will be
		// empty and the padding routine will refuse to pad it out to 12. So...
		if len(line) == 0 {
			line = " "
		}
		// Make the text fit the display exactly.
		line = padding.String(line, 12)
		if len(line) > 12 {
			line = line[:12]
		}
		lines[i] = line
	}
	//d.text = strings.Join(lines[:2], "")
	// fmt.Printf("%q: %q %q", text, lines[0], lines[1])

	return strings.Join(lines[:2], "")
}

// normalize will convert all runes to their closest ascii equivalents
func (d *Display) normalize(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	s, _, _ = transform.String(t, s)
	s = strings.ToLower(s)
	return s
}

func (d *Display) readStatus() error {
	ch := make(chan error)
	req := sendReq{
		msg: &proto.ToSplitflap{
			Payload: &proto.ToSplitflap_RequestState{},
		},
		ch: ch,
	}

	d.toDisplay <- req

	return <-ch
}

// Settings returns the current display settings.
func (d *Display) Settings() *proto.Settings {
	return d.lastStatus.Settings
}

// Text returns what the display is currently showing.
func (d *Display) Text() string {
	return d.text
}

// SetForceRotation turns the force_full_rotation setting on or off. If this
// setting is on, the display will go through a full rotation when the character
// for a cell is set to its current value.
func (d *Display) SetForceRotation(on bool) error {
	d.lastStatus.Settings.ForceFullRotation = on
	return d.sendConfigCmd()
}

// SetMaxMoving sets the maximum number of cells that are allowed to be moving
// at one time.
func (d *Display) SetMaxMoving(max uint32) error {
	d.lastStatus.Settings.MaxMoving = max
	return d.sendConfigCmd()
}

// SetStartDelay sets the delay between starting one module and the next, in
// milliseconds.
func (d *Display) SetStartDelay(delay uint32) error {
	d.lastStatus.Settings.StartDelayMillis = delay
	return d.sendConfigCmd()
}

// SetAnimStyle sets the animation style using the enum defined in the protobuf.
func (d *Display) SetAnimStyle(animStyle string) error {
	style, ok := proto.Settings_AnimationStyle_value[animStyle]
	if !ok {
		return errors.New("unknown animation style")
	}
	d.lastStatus.Settings.AnimationStyle = proto.Settings_AnimationStyle(style)
	return d.sendConfigCmd()
}

func (d *Display) sendConfigCmd() error {
	ch := make(chan error)
	req := sendReq{
		msg: &proto.ToSplitflap{
			Payload: &proto.ToSplitflap_SplitflapConfig{
				SplitflapConfig: &proto.SplitflapConfig{
					Settings: &proto.Settings{
						ForceFullRotation: d.lastStatus.Settings.GetForceFullRotation(),
						MaxMoving:         d.lastStatus.Settings.GetMaxMoving(),
						StartDelayMillis:  d.lastStatus.Settings.GetStartDelayMillis(),
						AnimationStyle:    d.lastStatus.Settings.GetAnimationStyle(),
					},
				},
			},
		},
		ch: ch,
	}

	d.toDisplay <- req
	return <-ch
}

// Status returns the current state of the display: how big it is, what it's
// showing, and error stats for each cell.
func (d *Display) Status() *proto.SplitflapState {
	return &d.lastStatus
}
