package e2e

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestEntrypointValidate(t *testing.T) {
	t.Run("direct agent", func(t *testing.T) {
		require.NoError(t, EntrypointDirectAgent.Validate())
	})

	t.Run("command rpc", func(t *testing.T) {
		require.NoError(t, EntrypointCommandRPC.Validate())
	})

	t.Run("empty", func(t *testing.T) {
		err := Entrypoint("").Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "e2e entrypoint is required")
	})

	t.Run("unsupported", func(t *testing.T) {
		err := Entrypoint("other").Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "unsupported e2e entrypoint")
	})
}

func TestRecommendedEntrypoints(t *testing.T) {
	assert.Equal(t, EntrypointDirectAgent, RecommendedPrimaryEntrypoint())
	assert.Equal(t, EntrypointCommandRPC, RecommendedSecondaryEntrypoint())
}

func TestConfigInputMode(t *testing.T) {
	tests := []struct {
		name string
		in   ConfigInput
		want ConfigMode
	}{
		{name: "env file", in: ConfigInput{EnvFilePath: ".env"}, want: ConfigModeRealInput},
		{name: "config file", in: ConfigInput{ConfigFilePath: "config.yaml"}, want: ConfigModeRealInput},
		{name: "env map", in: ConfigInput{Env: map[string]string{"A": "B"}}, want: ConfigModeRealInput},
		{name: "in memory", in: ConfigInput{}, want: ConfigModeInMemory},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.Mode())
		})
	}
}

func TestConfigInputValidate(t *testing.T) {
	t.Run("real input is valid", func(t *testing.T) {
		in := ConfigInput{ConfigFilePath: "config.yaml"}
		require.NoError(t, in.Validate())
	})

	t.Run("in memory allowed", func(t *testing.T) {
		in := ConfigInput{AllowInMemory: true}
		require.NoError(t, in.Validate())
	})

	t.Run("in memory not allowed", func(t *testing.T) {
		in := ConfigInput{}
		err := in.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "e2e config input requires real inputs or explicit in-memory fallback")
	})
}

func TestIsolationValidate(t *testing.T) {
	valid := Isolation{
		WorkspaceDir: "/tmp/work",
		DataDir:      "/tmp/data",
		StoragePath:  "/tmp/data/session.db",
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name string
		in   Isolation
		want string
	}{
		{name: "missing workspace", in: Isolation{DataDir: "/tmp/data", StoragePath: "/tmp/data/session.db"}, want: "e2e workspace dir is required"},
		{name: "missing data", in: Isolation{WorkspaceDir: "/tmp/work", StoragePath: "/tmp/data/session.db"}, want: "e2e data dir is required"},
		{name: "missing storage", in: Isolation{WorkspaceDir: "/tmp/work", DataDir: "/tmp/data"}, want: "e2e storage path is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			require.Error(t, err)
			assert.EqualError(t, err, tt.want)
		})
	}
}

func TestHarnessSpecValidate(t *testing.T) {
	valid := HarnessSpec{
		PrimaryEntrypoint:   EntrypointDirectAgent,
		SecondaryEntrypoint: EntrypointCommandRPC,
		Config:              ConfigInput{AllowInMemory: true},
		Isolation: Isolation{
			WorkspaceDir: "/tmp/work",
			DataDir:      "/tmp/data",
			StoragePath:  "/tmp/data/session.db",
		},
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name string
		in   HarnessSpec
		want string
	}{
		{
			name: "invalid primary",
			in: HarnessSpec{
				PrimaryEntrypoint:   "",
				SecondaryEntrypoint: EntrypointCommandRPC,
				Config:              ConfigInput{AllowInMemory: true},
				Isolation:           valid.Isolation,
			},
			want: "e2e entrypoint is required",
		},
		{
			name: "invalid secondary",
			in: HarnessSpec{
				PrimaryEntrypoint:   EntrypointDirectAgent,
				SecondaryEntrypoint: "",
				Config:              ConfigInput{AllowInMemory: true},
				Isolation:           valid.Isolation,
			},
			want: "e2e entrypoint is required",
		},
		{
			name: "same entrypoints",
			in: HarnessSpec{
				PrimaryEntrypoint:   EntrypointDirectAgent,
				SecondaryEntrypoint: EntrypointDirectAgent,
				Config:              ConfigInput{AllowInMemory: true},
				Isolation:           valid.Isolation,
			},
			want: "e2e primary and secondary entrypoints must differ",
		},
		{
			name: "wrong primary",
			in: HarnessSpec{
				PrimaryEntrypoint:   EntrypointCommandRPC,
				SecondaryEntrypoint: EntrypointDirectAgent,
				Config:              ConfigInput{AllowInMemory: true},
				Isolation:           valid.Isolation,
			},
			want: "e2e primary entrypoint must use the direct agent path",
		},
		{
			name: "bad config",
			in: HarnessSpec{
				PrimaryEntrypoint:   EntrypointDirectAgent,
				SecondaryEntrypoint: EntrypointCommandRPC,
				Config:              ConfigInput{},
				Isolation:           valid.Isolation,
			},
			want: "e2e config input requires real inputs or explicit in-memory fallback",
		},
		{
			name: "bad isolation",
			in: HarnessSpec{
				PrimaryEntrypoint:   EntrypointDirectAgent,
				SecondaryEntrypoint: EntrypointCommandRPC,
				Config:              ConfigInput{AllowInMemory: true},
				Isolation:           Isolation{},
			},
			want: "e2e workspace dir is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			require.Error(t, err)
			assert.EqualError(t, err, tt.want)
		})
	}

	t.Run("secondary explicit mismatch", func(t *testing.T) {
		in := valid
		in.SecondaryEntrypoint = Entrypoint("other")
		err := in.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "unsupported e2e entrypoint")
	})
}

func TestRootChatRequestValidate(t *testing.T) {
	require.NoError(t, (RootChatRequest{Message: "hello"}).Validate())

	err := (RootChatRequest{}).Validate()
	require.Error(t, err)
	assert.EqualError(t, err, "e2e root chat message is required")
}
