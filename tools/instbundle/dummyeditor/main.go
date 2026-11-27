// Command dummyeditor stands in for the editor (yakbd-gui.exe) in the installer bundle `instbundle -guest
// w11suite`: started, it writes its own path and working folder to C:\yk\results\dummy-editor-ran.txt and exits. It
// is a GUI-subsystem program for windows/amd64 and arm64, built by the bundle; nothing of the real editor is in it.
package main

import "os"

func main() {
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	_ = os.WriteFile(`C:\yk\results\dummy-editor-ran.txt`, []byte(exe+"\r\n"+wd+"\r\n"), 0o644)
}
