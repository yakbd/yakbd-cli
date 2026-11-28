package postable

import "testing"

func TestKnown(t *testing.T) {
	for p, want := range map[string]bool{"Break": true, "SysRq": true, "PrintScreen": true, "Lang1": true, "KeyA": true, "MyKey": false, "XkbFOO": false} {
		if Known(p) != want {
			t.Errorf("Known(%s) = %v", p, !want)
		}
	}
}
