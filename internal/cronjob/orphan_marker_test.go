package cronjob

import (
	"strings"
	"testing"
)

// A marker line whose job line is gone (the operator edited the crontab by
// hand) is followed by the next job's marker. RemoveByDomain used to skip the
// line after the matching marker without looking at it, which ate the next
// job's marker and left its job line behind as an unmanaged entry that no
// later List or RemoveByDomain could find; List read the same marker line as
// the orphan's job and reported a phantom job.
func TestOrphanMarkerDoesNotSwallowNextJobMarker(t *testing.T) {
	const crontab = "# UWAS managed [a.com] gone\n" +
		"# UWAS managed [b.com] keep\n" +
		"*/5 * * * * /bin/b-job\n" +
		"0 3 * * * /usr/bin/operator\n"

	g := &gatedCrontab{state: crontab, held: make(chan struct{}), release: make(chan struct{})}
	close(g.release)
	old := execCommandFn
	execCommandFn = g.command(t)
	defer func() { execCommandFn = old }()

	jobs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Domain != "b.com" || jobs[0].Command != "/bin/b-job" {
		t.Errorf("List = %+v, want only b.com /bin/b-job", jobs)
	}

	if err := RemoveByDomain("a.com"); err != nil {
		t.Fatal(err)
	}
	want := "# UWAS managed [b.com] keep\n*/5 * * * * /bin/b-job\n0 3 * * * /usr/bin/operator\n"
	if g.state != want {
		t.Errorf("crontab after RemoveByDomain(a.com):\n%s\nwant:\n%s", g.state, want)
	}

	// Control: a well-formed pair is still removed with its job line only.
	if err := RemoveByDomain("b.com"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.state, "b-job") || !strings.Contains(g.state, "operator") {
		t.Errorf("crontab after RemoveByDomain(b.com) = %q", g.state)
	}
}
