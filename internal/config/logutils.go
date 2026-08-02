package config

import (
	"path/filepath"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

func init() {
	logutils.SetConfigProvider(func() logutils.Config {
		cfg := Get()
		settings := logutils.Config{
			LogFile:    filepath.Join(datadir.HomeDir(), "morph.log"),
			MaxSizeMB:  constants.DefaultLogMaxSizeMB,
			MaxBackups: constants.DefaultLogMaxBackups,
			MaxAgeDays: constants.DefaultLogMaxAgeDays,
			Compress:   constants.DefaultLogCompress,
		}
		if cfg == nil {
			return settings
		}

		settings.NoColor = cfg.Log.NoColor
		fileValue := str.String(cfg.Log.File)
		if path := fileValue.Trim(); path != "" {
			settings.LogFile = path
		}
		settings.MaxSizeMB = cfg.Log.MaxSizeMB
		settings.MaxBackups = cfg.Log.MaxBackups
		settings.MaxAgeDays = cfg.Log.MaxAgeDays
		settings.Compress = cfg.Log.Compress

		return settings
	})
}
