package loading

import (
	"fmt"
	"sync"
	"time"
)

type Spinner struct {
	message  string
	frames   []string
	mu       sync.Mutex
	running  bool
	stopChan chan struct{}
}

var defaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func New(message string) *Spinner {
	return &Spinner{
		message:  message,
		frames:   defaultFrames,
		stopChan: make(chan struct{}),
	}
}

func (s *Spinner) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		frameIdx := 0
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-s.stopChan:
				return
			case <-ticker.C:
				s.mu.Lock()
				frame := s.frames[frameIdx]
				s.mu.Unlock()

				fmt.Printf("\r%s %s", frame, s.message)
				frameIdx = (frameIdx + 1) % len(s.frames)
			}
		}
	}()
}

func (s *Spinner) Stop(message string) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	s.stopChan <- struct{}{}

	if message != "" {
		fmt.Printf("\r%s %s\n", "✅", message)
	} else {
		fmt.Print("\r")
		for i := 0; i <= len(s.message)+10; i++ {
			fmt.Print(" ")
		}
		fmt.Print("\r")
	}
}

func WithMessage(msg string) *Spinner {
	return New(msg)
}
