package supervisor

import (
	"errors"
	"os/exec"
	"sync"
	"time"
)

var errNoActiveProviderProcess = errors.New("no provider process is running")

const interruptGracePeriod = 2 * time.Second

// processControl owns the operating-system fallback for one provider process.
// The provider-native interrupt may be enough, but a broken provider or a
// child that ignores the graceful signal must not leave the supervisor stuck.
type processControl struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	done        chan struct{}
	interrupted bool
}

func (p *processControl) attach(cmd *exec.Cmd) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cmd = cmd
	p.done = make(chan struct{})
	p.interrupted = false
}

func (p *processControl) clear(cmd *exec.Cmd) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != cmd {
		return
	}
	if p.done != nil {
		close(p.done)
	}
	p.cmd = nil
	p.done = nil
	p.interrupted = false
}

// interrupt sends the platform's graceful interrupt and schedules a hard
// process-group kill as a safety net. It is idempotent for one turn.
func (p *processControl) interrupt() error {
	p.mu.Lock()
	if p.cmd == nil {
		p.mu.Unlock()
		return errNoActiveProviderProcess
	}
	if p.interrupted {
		p.mu.Unlock()
		return nil
	}
	p.interrupted = true
	cmd, done := p.cmd, p.done
	p.mu.Unlock()

	if err := interruptProcess(cmd); err != nil {
		p.mu.Lock()
		if p.cmd == cmd {
			p.interrupted = false
		}
		p.mu.Unlock()
		return err
	}
	p.killAfterGrace(cmd, done)
	return nil
}

// scheduleKill is used when the provider accepted a native interrupt request
// but the request itself does not involve an operating-system signal.
func (p *processControl) scheduleKill() {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd != nil {
		p.killAfterGrace(cmd, done)
	}
}

func (p *processControl) killAfterGrace(cmd *exec.Cmd, done <-chan struct{}) {
	go func() {
		timer := time.NewTimer(interruptGracePeriod)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}
		p.mu.Lock()
		stillRunning := p.cmd == cmd
		p.mu.Unlock()
		if stillRunning {
			_ = killProcess(cmd)
		}
	}()
}
