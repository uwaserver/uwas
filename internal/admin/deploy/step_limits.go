package deploy

import (
	"os/exec"
	"time"
)

// maxStepOutput caps the output of one deploy step kept for the log. A build
// runs tenant repo code, and its output used to be buffered whole in memory
// before it reached the log, so a chatty or runaway script could exhaust the
// server's memory (F56000).
const maxStepOutput = 2 << 20

// stepPipeWaitDelay bounds how long a finished (or killed) step waits for its
// output pipe to close: a child the step left running holds the pipe, and
// cmd.Run would wait for it, hanging the deploy and the per-app lock (F56001).
const stepPipeWaitDelay = 2 * time.Second

const truncatedNote = "...[earlier output truncated]\n"

// tailBuffer keeps the last max bytes written. Builds report their failure at
// the end, so the tail is the part worth keeping. It still reports full writes
// so the command is never blocked on its pipe.
type tailBuffer struct {
	max       int
	buf       []byte
	truncated bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.cut()
	}
	return len(p), nil
}

func (t *tailBuffer) cut() {
	t.truncated = true
	n := copy(t.buf, t.buf[len(t.buf)-t.max:])
	t.buf = t.buf[:n]
}

// Bytes returns the kept tail, prefixed with a note when output was dropped.
func (t *tailBuffer) Bytes() []byte {
	if len(t.buf) > t.max {
		t.cut()
	}
	if !t.truncated {
		return t.buf
	}
	return append([]byte(truncatedNote), t.buf...)
}

// confineStep makes a step's whole process tree die with its context and stops
// the step waiting on pipes a leftover descendant keeps open.
func confineStep(cmd *exec.Cmd) {
	setStepProcessGroup(cmd)
	cmd.Cancel = func() error { return killStepProcessGroup(cmd) }
	cmd.WaitDelay = stepPipeWaitDelay
}
