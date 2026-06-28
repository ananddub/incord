package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestModuleGraph(t *testing.T) {
	require.NoError(t, fx.ValidateApp(Module))
}
