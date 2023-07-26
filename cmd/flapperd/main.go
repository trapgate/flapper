// Package main implements flapperd, the splitflap display daemon. This runs on
// the system directly connected to the splitflap display via USB, and exposes
// http endpoints for controlling the display.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/trapgate/flapper"
	"github.com/trapgate/flapper/idle"
)

var (
	errNoFormValue = errors.New("form value not set")
)

type serveCmd struct {
	d     *flapper.Display
	idler idle.Display
}

type displayCmd struct {
	Text string `arg:"" name:"text" help:"String to display."`
}

type statusCmd struct {
}

var cli struct {
	Serve   serveCmd   `cmd:"" help:"Listen on http for strings to display." default:"1"`
	Display displayCmd `cmd:"" help:"Display a string on the splitflaps."`
	Status  statusCmd  `cmd:"" help:"Display the status of the splitflaps."`
}

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run(ctx)
	ctx.FatalIfErrorf(err)
}

func (c *serveCmd) Run(ctx *kong.Context) error {
	d, err := flapper.NewDisplay()
	if err != nil {
		return err
	}
	c.d = d

	fmt.Println("listening on port 8080")
	http.HandleFunc("/text", c.httpText)
	http.HandleFunc("/status", c.httpStatus)
	http.HandleFunc("/idle", c.httpIdle)

	// Set up the "screensaver"
	c.idler = idle.NewQuakeMon(10 * time.Minute)
	idlerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.idler.Run(idlerCtx, d)

	err = http.ListenAndServe(":8080", nil)
	fmt.Println(err)
	return err
}

func (c *serveCmd) httpText(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		text := c.d.Text()
		if len(text) > 12 {
			text = text[:12] + "\n" + text[12:]
		}
		fmt.Fprintf(w, "%v", text)
	case http.MethodPost:
		c.idler.Reset()
		// maxmoving will limit the number of displays that animate at a time.
		if maxMoving, err := readFormUint(r, "maxmoving"); err != errNoFormValue {
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			err = c.d.SetMaxMoving(uint32(maxMoving))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		// fullrotation specifies whether cells that are not changing are still
		// moved.
		if fullRotation, err := readFormBool(r, "fullrotation"); err != errNoFormValue {
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			err = c.d.SetForceRotation(fullRotation)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}

		// startdelay specifies the number of milliseconds to delay between
		// starting modules moving.
		if startDelay, err := readFormUint(r, "startdelay"); err != errNoFormValue {
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			err = c.d.SetStartDelay(uint32(startDelay))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}

		// animStyle specifies what order to start the modules in. It will have
		// no visible effect unless startdelay or maxmoving is also set.
		if animStyle, err := readFormString(r, "animstyle"); err != errNoFormValue {
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			err = c.d.SetAnimStyle(animStyle)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}

		// Features to add:
		// - Move the word left across the display. Start the letters of the
		//   word and the cell to the left animating so that they finish at the
		//   same time.
		// - Fall letters in from the top row to the bottom.

		// For multi-line text, delay between each line.
		delay := 5 * time.Second
		delayStr := r.PostFormValue("delay")
		if delayStr != "" {
			delaySecs, err := strconv.Atoi(delayStr)
			delay = time.Duration(delaySecs) * time.Second
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		lines := strings.Split(r.PostFormValue("text"), "\n")
		fmt.Println(lines)
		for i, line := range lines {
			err := c.d.SetText(line)
			if err != nil {
				fmt.Println(err)
			}
			if i+1 < len(lines) {
				time.Sleep(delay)
			}
		}
	}
}

func (c *serveCmd) httpStatus(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		fmt.Fprintf(w, "%v", c.d.Status())
	}
}

func (c *serveCmd) httpIdle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		fmt.Fprintf(w, "%v", c.idler.Name())
	case http.MethodPost:
		if enable, err := readFormBool(r, "enable"); err != errNoFormValue {
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			c.idler.Enable(enable)
		}
		// TODO: allow the delay and the idler name to be set.
	}
}

func readFormInt(r *http.Request, valName string) (int, error) {
	valStr := r.PostFormValue(valName)
	if valStr == "" {
		return 0, errNoFormValue
	}
	val, err := strconv.ParseInt(valStr, 10, 32)
	if err != nil {
		return 0, err
	}
	return int(val), nil
}

func readFormUint(r *http.Request, valName string) (uint, error) {
	valStr := r.PostFormValue(valName)
	if valStr == "" {
		return 0, errNoFormValue
	}
	val, err := strconv.ParseUint(valStr, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint(val), nil
}

// readFormBool will attempt to read a boolean value from a form. It will return
// errNoFormValue if the specified value name wasn't sent.
func readFormBool(r *http.Request, valName string) (bool, error) {
	valStr := r.PostFormValue(valName)
	if valStr == "" {
		return false, errNoFormValue
	}
	switch strings.ToLower(valStr) {
	case "true", "on":
		return true, nil
	case "false", "off":
		return false, nil
	default:
		return false, errors.New("invalid boolean value in form")
	}
}

func readFormString(r *http.Request, valName string) (string, error) {
	valStr := r.PostFormValue(valName)
	if valStr == "" {
		return "", errNoFormValue
	}
	return valStr, nil
}

func (c *displayCmd) Run(ctx *kong.Context) error {
	d, err := flapper.NewDisplay()
	if err != nil {
		return err
	}
	time.Sleep(5 * time.Second)
	d.SetText(c.Text)
	// Temporarily:
	time.Sleep(5 * time.Second)

	return nil
}

func (c *statusCmd) Run(ctx *kong.Context) error {
	d, err := flapper.NewDisplay()
	if err != nil {
		return err
	}
	d.Init()
	time.Sleep(5 * time.Second)
	return nil
}
