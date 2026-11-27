//go:build !windows

package main

import "errors"

func newSystem() (system, error) {
	return nil, errors.New("runs only on Windows, inside a disposable virtual machine")
}
