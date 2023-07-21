package idle

import (
	"context"

	"github.com/trapgate/flapper"
)

type Display interface {
	Enable(bool)
	Run(context.Context, *flapper.Display)
	Reset()
	Name() string
}
