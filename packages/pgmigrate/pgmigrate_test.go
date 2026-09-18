package pgmigrate

import "testing"

// The filename is the version, and sorting the filenames is what orders them. A
// name that does not fit has to stop the server rather than run in whatever
// order it happens to sort into.
func TestVersionOfRejectsMisnamedMigrations(t *testing.T) {
	tests := map[string]bool{
		"0001_initial.sql":      true,
		"0012_add_a_column.sql": true,
		"1_initial.sql":         false,
		"001_initial.sql":       false,
		"00012_initial.sql":     false,
		"initial.sql":           false,
		"abcd_initial.sql":      false,
		"0001-initial.sql":      false,
		"_0001_initial.sql":     false,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := VersionOf(name)
			if (err == nil) != want {
				t.Errorf("VersionOf(%q) error = %v, want an error: %v", name, err, !want)
			}
		})
	}
}

func TestVersionOfReadsTheNumber(t *testing.T) {
	for name, want := range map[string]int{
		"0001_init.sql":         1,
		"0042_add_a_column.sql": 42,
		"9999_the_last_one.sql": 9999,
	} {
		got, err := VersionOf(name)
		if err != nil {
			t.Errorf("VersionOf(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("VersionOf(%q) = %d, want %d", name, got, want)
		}
	}
}
