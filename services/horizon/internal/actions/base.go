package actions

import (
	"net/http"

	gctx "github.com/goji/context"

	"github.com/kinecosystem/go/services/horizon/internal/render"
	hProblem "github.com/kinecosystem/go/services/horizon/internal/render/problem"
	"github.com/kinecosystem/go/services/horizon/internal/render/sse"
	"github.com/kinecosystem/go/support/render/problem"
	"github.com/zenazn/goji/web"
	"golang.org/x/net/context"
)

// Base is a helper struct you can use as part of a custom action via
// composition.
//
// TODO: example usage
type Base struct {
	Ctx     context.Context
	GojiCtx web.C
	W       http.ResponseWriter
	R       *http.Request
	Err     error

	isSetup bool
}

// Prepare established the common attributes that get used in nearly every
// action.  "Child" actions may override this method to extend action, but it
// is advised you also call this implementation to maintain behavior.
func (base *Base) Prepare(c web.C, w http.ResponseWriter, r *http.Request) {
	base.Ctx = gctx.FromC(c)
	base.GojiCtx = c
	base.W = w
	base.R = r
}

// Execute trigger content negottion and the actual execution of one of the
// action's handlers.
func (base *Base) Execute(action interface{}) {
	contentType := render.Negotiate(base.Ctx, base.R)

	switch contentType {
	case render.MimeHal, render.MimeJSON:
		action, ok := action.(JSON)

		if !ok {
			goto NotAcceptable
		}

		action.JSON()

		if base.Err != nil {
			problem.Render(base.Ctx, base.W, base.Err)
			return
		}

	case render.MimeEventStream:
		var pumped chan interface{}

		action, ok := action.(SSE)
		if !ok {
			goto NotAcceptable
		}

		// Subscribe this handler to the topic if the SSE request is related to a specific topic (tx_id, account_id, etc.).
		// This causes action.SSE to only be triggered by this topic. Unsubscribe when done.
		topic := action.GetTopic()
		if topic != "" {
			pumped = sse.Subscribe(topic)
			defer sse.Unsubscribe(pumped, topic)
		}

		stream := sse.NewStream(base.Ctx, base.W, base.R)

		for {
			action.SSE(stream)

			if base.Err != nil {
				// in the case that we haven't yet sent an event, is also means we
				// havent sent the preamble, meaning we should simply return the normal
				// error.
				if stream.SentCount() == 0 {
					problem.Render(base.Ctx, base.W, base.Err)
					return
				}

				stream.Err(base.Err)
			}

			if stream.IsDone() {
				return
			}

			// Reply preamble message that states delay timer.
			//
			// NOTE This only happens only healthy HTTP 2xx responses.
			//
			// This is a hacky solution that tries to solve the case where clients have an unwanted behavior:
			// They already have an account open and just want to poll their account balance but retry too quickly,
			// according to default retry value (a few seconds usually). This causes them to spam the server right
			// after disconnecting the SSE stream.
			//
			// This change will cause them to receive the preamble over and over again on every SSE tick,
			// practically turning it into a keepalive message. This would lower the reconnection amount
			// and in turn (most importantly) the amount of queries sent to the Horizon database.
			//
			// This has a cost though - it keeps SSE connections alive and also causes "junk" keep alive traffic
			// to be sent over and over again.
			sse.WritePreamble(base.Ctx, base.W)

			select {
			case <-base.Ctx.Done():
				return
			case <-pumped:
				//no-op, continue onto the next iteration
			}
		}
	case render.MimeRaw:
		action, ok := action.(Raw)

		if !ok {
			goto NotAcceptable
		}

		action.Raw()

		if base.Err != nil {
			problem.Render(base.Ctx, base.W, base.Err)
			return
		}
	default:
		goto NotAcceptable
	}
	return

NotAcceptable:
	problem.Render(base.Ctx, base.W, hProblem.NotAcceptable)
	return
}

// Do executes the provided func iff there is no current error for the action.
// Provides a nicer way to invoke a set of steps that each may set `action.Err`
// during execution
func (base *Base) Do(fns ...func()) {
	for _, fn := range fns {
		if base.Err != nil {
			return
		}

		fn()
	}
}

// Setup runs the provided funcs if and only if no call to Setup() has been
// made previously on this action.
func (base *Base) Setup(fns ...func()) {
	if base.isSetup {
		return
	}
	base.Do(fns...)
	base.isSetup = true
}
