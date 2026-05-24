package api

import (
	"io"
	"os"
)

// defaultStderr is the production sink for verbose logs. Tests override
// the `stderr` indirection variable in client.go to capture output.
var defaultStderr io.Writer = os.Stderr
