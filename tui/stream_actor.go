package tui

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// StreamActor manages the lifecycle of a streaming goroutine.
// It replaces the manual streamChan + streamDoneCh + sync.Once pattern
// that was duplicated across startGenerateCmd and execPendingCmd.
type StreamActor struct {
	ch   chan tea.Msg
	stop chan struct{}
	once sync.Once
}

func NewStreamActor() *StreamActor {
	return &StreamActor{
		ch:   make(chan tea.Msg, 64),
		stop: make(chan struct{}),
	}
}

// Run starts fn in a goroutine, passing a send function it can use to
// deliver messages. Returns a tea.Cmd that reads from the internal channel.
// Stop() signals the goroutine to exit.
func (a *StreamActor) Run(fn func(send func(tea.Msg))) tea.Cmd {
	go func() {
		defer close(a.ch)
		fn(func(msg tea.Msg) {
			select {
			case a.ch <- msg:
			case <-a.stop:
			}
		})
	}()
	return a.NextMsgCmd()
}

// NextMsg returns a tea.Cmd that reads the next message from the channel.
func (a *StreamActor) NextMsg() tea.Msg {
	select {
	case msg, ok := <-a.ch:
		if !ok {
			return nil
		}
		return msg
	case <-a.stop:
		return nil
	}
}

// Stop signals the goroutine to exit. Safe to call multiple times.
func (a *StreamActor) Stop() {
	a.once.Do(func() { close(a.stop) })
}

// NextMsgCmd returns a tea.Cmd that reads from the actor channel.
func (a *StreamActor) NextMsgCmd() tea.Cmd {
	return func() tea.Msg { return a.NextMsg() }
}
