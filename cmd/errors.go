package cmd

import "errors"

// ErrAlreadyPresented is returned by RunE implementations that have already
// written formatted output (via the presenter or directly). main.go's run()
// recognises this sentinel and exits non-zero without writing a second block.
var ErrAlreadyPresented = errors.New("already presented")
