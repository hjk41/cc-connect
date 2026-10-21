package antigravity

import (
	"os"
	"testing"

	"github.com/chenhg5/cc-connect/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunShellHelper(); os.Exit(m.Run()) }
