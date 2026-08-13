package state

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv(StateDirEnv)
	os.Exit(m.Run())
}
