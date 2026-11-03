package apps

import "testing"

func TestValidateVolumesRejectsWindowsAbsolutePaths(t *testing.T) {
	if err := ValidateVolumes([]string{"data:/data"}); err != nil {
		t.Fatalf("named volume rejected: %v", err)
	}
	for _, volume := range []string{
		`C:\Windows\System32:/host`,
		`D:/secrets:/data:ro`,
		`\\server\share:/mnt`,
	} {
		t.Run(volume, func(t *testing.T) {
			if err := ValidateVolumes([]string{volume}); err == nil {
				t.Fatalf("ValidateVolumes(%q) returned nil", volume)
			}
		})
	}
}
