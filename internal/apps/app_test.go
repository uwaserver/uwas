package apps

import "testing"

// dockerExtraArgBypasses are single-token arguments that a one-dash reader
// would treat as a flag. Each is a short form of a capability the allowlist
// documents itself as excluding, and each is self-contained: it needs no
// following value, so nothing else in the loop inspects it.
//
// "-v/:/host" and "-v=/:/host" are the sharp edge — a single argv element that
// docker parses as a volume mount of the host root, which is the whole
// container-isolation boundary ValidateExtraArgs exists to hold.
var dockerExtraArgBypasses = [][]string{
	{"-v/:/host"},
	{"-v=/:/host"},
	{"-privileged"},
	{"-net=host"},
	{"-security-opt=seccomp=unconfined"},
	{"-pid=host"},
	{"-ipc=host"},
	{"-uts=host"},
	{"-cap-add=NET_ADMIN"},
}

// TestValidateExtraArgsRejectsSingleDashFlags pins the isolation contract for
// docker.extra_args.
//
// ValidateExtraArgs documents that it excludes "-v/--volume" among the
// host-level capabilities, but only the long "--" spellings were ever checked:
// an argument with a single dash matched neither the allowlist branch nor the
// positional-argument branch, so it fell through the loop unexamined and
// docker.go appended it verbatim to the `docker run` argv. A self-contained
// "-v/:/host" therefore mounted the host root, with the two-element "-v /:/host"
// form rejected only incidentally (its value tripped the positional check).
func TestValidateExtraArgsRejectsSingleDashFlags(t *testing.T) {
	for _, args := range dockerExtraArgBypasses {
		if err := ValidateExtraArgs(args); err == nil {
			t.Errorf("ValidateExtraArgs accepted %q: a single-dash flag matched "+
				"neither rejecting branch and was passed to the docker CLI verbatim, "+
				"granting a host-level capability the allowlist excludes", args)
		}
	}
}

// TestValidateExtraArgsRejectsShortFlagWithValue covers the separated form. It
// was already rejected, but only because the value tripped the
// positional-argument check — the flag itself was never examined. It is pinned
// here so the rejection is attributable to the flag, not to luck.
func TestValidateExtraArgsRejectsShortFlagWithValue(t *testing.T) {
	cases := [][]string{
		{"-v", "/:/host"},
		{"-privileged"},
		{"-cap-add", "NET_ADMIN"},
		{"-net", "host"},
		{"-pid", "host"},
	}
	for _, args := range cases {
		if err := ValidateExtraArgs(args); err == nil {
			t.Errorf("ValidateExtraArgs accepted %q", args)
		}
	}
}

// TestValidateExtraArgsRejectsLongDangerousFlags is the control for the forms
// that already worked; they must keep failing after the fix.
func TestValidateExtraArgsRejectsLongDangerousFlags(t *testing.T) {
	cases := [][]string{
		{"--volume", "/:/host"},
		{"--volume=/:/host"},
		{"--privileged"},
		{"--cap-add", "NET_ADMIN"},
		{"--network", "host"},
		{"--security-opt", "seccomp=unconfined"},
		{"--pid", "host"},
		{"--ipc", "host"},
		{"--uts", "host"},
		{"--entrypoint", "/bin/sh"},
	}
	for _, args := range cases {
		if err := ValidateExtraArgs(args); err == nil {
			t.Errorf("ValidateExtraArgs accepted long form %q", args)
		}
	}
}

// TestValidateExtraArgsAcceptsAllowlistedFlags is the second control: the fix
// must not degenerate into rejecting everything, and the "=" spelling of an
// allowlisted long flag must keep working.
func TestValidateExtraArgsAcceptsAllowlistedFlags(t *testing.T) {
	cases := [][]string{
		{"--init"},
		{"--read-only"},
		{"--memory", "512m"},
		{"--memory=512m"},
		{"--cpus", "1.5"},
		{"--restart", "unless-stopped"},
		{"--user", "1001:1001"},
		{"--env", "A=1"},
		{"--env=A=1"},
		{},
	}
	for _, args := range cases {
		if err := ValidateExtraArgs(args); err != nil {
			t.Errorf("allowlisted %q was rejected: %v", args, err)
		}
	}
}

// TestValidateVolumesRejectsHostMounts is the neighbouring control: the sibling
// guard for the volumes field already closed this escape, and must stay closed.
func TestValidateVolumesRejectsHostMounts(t *testing.T) {
	bad := []string{
		"/:/host",
		"/etc:/data",
		"/var/run/docker.sock:/sock",
		"../../../etc:/data",
		"data/../../:/data",
	}
	for _, v := range bad {
		if err := ValidateVolumes([]string{v}); err == nil {
			t.Errorf("ValidateVolumes accepted %q", v)
		}
	}
	good := []string{"data:/data", "myvolume:/var/www", "./local:/app"}
	for _, v := range good {
		if err := ValidateVolumes([]string{v}); err != nil {
			t.Errorf("ValidateVolumes rejected safe volume %q: %v", v, err)
		}
	}
}
