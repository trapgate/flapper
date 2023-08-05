package idle

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/trapgate/flapper"
	quake "github.com/trapgate/go-quake"
)

const (
	// This is how often the idler will fetch new quake data and see if the
	// display needs updated.
	updateInterval = 5 * time.Minute
)

// byMag is for sorting the returned list of quakes by magnitude.
type byMag []quake.Feature

func (q byMag) Len() int {
	return len(q)
}

func (q byMag) Less(i, j int) bool {
	// Break ties using the time of the quake instead. The display will show the
	// most recent quake if there's a tie for the highest magnitude.
	if q[i].Properties.Magnitude == q[j].Properties.Magnitude {
		return q[i].Properties.Time < q[j].Properties.Time
	}
	return q[i].Properties.Magnitude < q[j].Properties.Magnitude
}

func (q byMag) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

// QuakeMon is an earthquake monitor. It uses the USGS earthquake feed, as
// implemented in the usgsquake package.
type QuakeMon struct {
	startDelay      time.Duration
	currentQuakeURL string
	resetCh         chan struct{}
	enableCh        chan bool
}

// NewQuakeMon returns a quake idler which will display the most recent largest
// quake from the past day.
func NewQuakeMon(startDelay time.Duration) *QuakeMon {
	return &QuakeMon{
		startDelay: startDelay,
		resetCh:    make(chan struct{}),
		enableCh:   make(chan bool),
	}
}

// Name returns the name of the idler.
func (q *QuakeMon) Name() string {
	return "Earthquake Monitor"
}

// Run is called when this is the active idler. It does nothing until the
// startDelay expires, then it will set the splitflap to display the latest
// quake. This routine can be cancelled using the passed in context.
func (q *QuakeMon) Run(ctx context.Context, display *flapper.Display) {
	showing := false
	enable := true
	t := time.NewTimer(q.startDelay)
	for {
		delay := q.startDelay
		if showing {
			delay = updateInterval
		}
		if enable {
			t.Reset(delay)
		}

		select {
		case <-t.C:
			quakes, err := quake.Fetch(quake.Mag4_5, quake.Day)
			if err != nil {
				fmt.Println("failed to fetch quake list", err)
			}
			q.print(display, quakes)
			showing = true
		case <-q.resetCh:
			if enable && !t.Stop() {
				<-t.C
			}
			q.currentQuakeURL = ""
			showing = false
		case e := <-q.enableCh:
			if enable && !t.Stop() {
				<-t.C
			}
			enable = e
		case <-ctx.Done():
			if enable && !t.Stop() {
				<-t.C
			}
			fmt.Println("quake idler shutting down")
			return
		}
	}
}

// Enable is used to enable or disable the idler.
func (q *QuakeMon) Enable(enable bool) {
	q.enableCh <- enable
}

// Reset is called when something else is sent to the splitflap display. It
// resets the startDelay.
func (q *QuakeMon) Reset() {
	q.resetCh <- struct{}{}
}

func (q *QuakeMon) print(display *flapper.Display, quakes quake.QuakeList) error {
	sort.Sort(sort.Reverse(byMag(quakes.Features)))

	// Display the largest quake
	desc := fmt.Sprintf("%v %v",
		quakes.Features[0].Properties.Magnitude,
		quakes.Features[0].Properties.Place)

	// For some reason the Place field (and the title too) sometimes changes
	// on subsequent polls. Remember the URL of the displayed quake so we don't
	// just switch back and forth.
	if q.currentQuakeURL == quakes.Features[0].Properties.URL {
		return nil
	}
	q.currentQuakeURL = quakes.Features[0].Properties.URL

	desc = truncate(desc)
	fmt.Println("quake monitor text:", desc)
	return display.SetText(desc)
}

var abbrevs = map[string]string{
	"north":     "n",
	"south":     "s",
	"east":      "e",
	"west":      "w",
	"northeast": "ne",
	"northwest": "nw",
	"southeast": "se",
	"southwest": "sw",
	"islands":   "is.",
}

// truncate removes some specific data and uses abbreviations to shorten the
// place name as much as possible.
func truncate(desc string) string {
	// Get rid of strings like, '150 km NE of ' from the place.
	re := regexp.MustCompile(`\d+ km [NSEW]+ of `)
	desc = re.ReplaceAllString(desc, "")
	words := strings.Split(desc, " ")
	for i := range words {
		rep, ok := abbrevs[strings.ToLower(words[i])]
		if ok {
			words[i] = rep
		}
	}
	desc = strings.Join(words, " ")
	return desc
}
