// Copyright 2020 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package events

import (
	"net/http"
	"time"

	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/eventsource"
	"code.gitea.io/gitea/modules/graceful"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/routers/user"
)

// Events listens for events
func Events(ctx *context.Context) {
	// FIXME: Need to check if resp is actually a http.Flusher! - how though?

	// Set the headers related to event streaming.
	ctx.Resp.Header().Set("Content-Type", "text/event-stream")
	ctx.Resp.Header().Set("Cache-Control", "no-cache")
	ctx.Resp.Header().Set("Connection", "keep-alive")
	ctx.Resp.Header().Set("X-Accel-Buffering", "no")
	ctx.Resp.WriteHeader(http.StatusOK)

	// Listen to connection close and un-register messageChan
	notify := ctx.Req.Context().Done()
	ctx.Resp.Flush()

	shutdownCtx := graceful.GetManager().ShutdownContext()

	uid := ctx.User.ID

	messageChan := eventsource.GetManager().Register(uid)

	unregister := func() {
		eventsource.GetManager().Unregister(uid, messageChan)
		// ensure the messageChan is closed
		for {
			_, ok := <-messageChan
			if !ok {
				break
			}
		}
	}

	if _, err := ctx.Resp.Write([]byte("\n")); err != nil {
		log.Error("Unable to write to EventStream: %v", err)
		unregister()
		return
	}

	timer := time.NewTicker(30 * time.Second)

loop:
	for {
		select {
		case <-timer.C:
			event := &eventsource.Event{
				Name: "ping",
			}
			_, err := event.WriteTo(ctx.Resp)
			if err != nil {
				log.Error("Unable to write to EventStream for user %s: %v", ctx.User.Name, err)
				go unregister()
				break loop
			}
			ctx.Resp.Flush()
		case <-notify:
			go unregister()
			break loop
		case <-shutdownCtx.Done():
			go unregister()
			break loop
		case event, ok := <-messageChan:
			if !ok {
				break loop
			}

			// Handle logout
			if event.Name == "logout" {
				if ctx.Session.ID() == event.Data {
					_, _ = (&eventsource.Event{
						Name: "logout",
						Data: "here",
					}).WriteTo(ctx.Resp)
					ctx.Resp.Flush()
					go unregister()
					user.HandleSignOut(ctx)
					break loop
				}
				// Replace the event - we don't want to expose the session ID to the user
				event = (&eventsource.Event{
					Name: "logout",
					Data: "elsewhere",
				})
			}

			_, err := event.WriteTo(ctx.Resp)
			if err != nil {
				log.Error("Unable to write to EventStream for user %s: %v", ctx.User.Name, err)
				go unregister()
				break loop
			}
			ctx.Resp.Flush()
		}
	}
	timer.Stop()
}
