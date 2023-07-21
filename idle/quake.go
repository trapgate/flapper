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
	updateInterval = 2 * time.Minute
)

type byMag []quake.Feature

func (q byMag) Len() int {
	return len(q)
}

func (q byMag) Less(i, j int) bool {
	// Break ties using the place instead.
	if q[i].Properties.Magnitude == q[j].Properties.Magnitude {
		return q[i].Properties.Place < q[j].Properties.Place
	}
	return q[i].Properties.Magnitude < q[j].Properties.Magnitude
}

func (q byMag) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

// QuakeMon is an earthquake monitor. It uses the USGS earthquake feed, as
// implemented in the usgsquake package.
type QuakeMon struct {
	startDelay time.Duration
	resetCh    chan struct{}
	enableCh   chan bool
}

func NewQuakeMon(startDelay time.Duration) QuakeMon {
	return QuakeMon{
		startDelay: startDelay,
		resetCh:    make(chan struct{}),
		enableCh:   make(chan bool),
	}
}

func (q QuakeMon) Name() string {
	return "Earthquake Monitor"
}

func (q QuakeMon) Run(ctx context.Context, display *flapper.Display) {
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

		fmt.Println("+++idler is waiting")
		select {
		case <-t.C:
			quakes, err := quake.Fetch(quake.Mag4_5, quake.Day)
			if err != nil {
				fmt.Println("failed to fetch quake list", err)
			}
			q.print(display, quakes)
			showing = true
		case <-q.resetCh:
			showing = false
			if enable && !t.Stop() {
				<-t.C
			}
		case e := <-q.enableCh:
			if enable && !t.Stop() {
				<-t.C
			}
			enable = e
		case <-ctx.Done():
			return
		}
	}
}

func (q QuakeMon) Enable(enable bool) {
	q.enableCh <- enable
}

func (q QuakeMon) Reset() {
	q.resetCh <- struct{}{}
}

func (q QuakeMon) print(display *flapper.Display, quakes quake.QuakeList) error {
	sort.Sort(sort.Reverse(byMag(quakes.Features)))

	// Display the largest quake
	desc := fmt.Sprintf("%v %v",
		quakes.Features[0].Properties.Magnitude,
		quakes.Features[0].Properties.Place)

	desc = truncate(desc)

	return display.SetText(desc)
}

var abbrevs = map[string]string{
	"North":   "N",
	"South":   "S",
	"East":    "E",
	"West":    "W",
	"Islands": "Is.",
}

// truncate removes some specific data and uses abbreviations to shorten the
// place name as much as possible.
func truncate(desc string) string {
	// Get rid of strings like, '150 km NE of ' from the place.
	re := regexp.MustCompile(`\d+ km [NSEW]+ of `)
	desc = re.ReplaceAllString(desc, "")
	words := strings.Split(desc, " ")
	for i := range words {
		rep, ok := abbrevs[words[i]]
		if ok {
			words[i] = rep
		}
	}
	desc = strings.Join(words, " ")
	return desc
}
