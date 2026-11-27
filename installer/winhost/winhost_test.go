package winhost

import "testing"

func TestBootPath(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{`C:\Windows\Sysnative\kbdtst.dll`, `C:\Windows\System32\kbdtst.dll`, true},
		{`c:\windows\SYSNATIVE\kbdtst.dll.yakbd-new`, `C:\Windows\System32\kbdtst.dll.yakbd-new`, true},
		{`C:\Windows\SysWOW64\kbdtst.dll`, `C:\Windows\SysWOW64\kbdtst.dll`, false},
		{`C:\Windows\System32\kbdtst.dll`, `C:\Windows\System32\kbdtst.dll`, false},
		{`C:\Windows\Sysnative\`, `C:\Windows\Sysnative\`, false},
		{`C:\Users\a\AppData\Local\Temp\x.exe`, `C:\Users\a\AppData\Local\Temp\x.exe`, false},
	} {
		if got, ok := BootPath(c.in, `C:\Windows\`); got != c.want || ok != c.ok {
			t.Errorf("BootPath(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
