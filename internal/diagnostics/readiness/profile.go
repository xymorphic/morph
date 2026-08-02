package readiness

import (
	"fmt"
	"os"

	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/str"
)

var statPath = os.Stat

func buildProfileGroup(active profile.Profile, envPath string, configPath string) Group {
	active = profile.WithMetadataPaths(active)
	envPathValue := str.String(envPath)
	if envPathValue.Trim() != "" {
		envPathValue2 := str.String(envPath)
		active.EnvPath = envPathValue2.Trim()
	}
	configPathValue := str.String(configPath)
	if configPathValue.Trim() != "" {
		configPathValue2 := str.String(configPath)
		active.ConfigPath = configPathValue2.Trim()
	}

	return Group{
		Name: "profile",
		Checks: []Check{
			check("name", StatusPass, fmt.Sprintf("using profile %q", defaultString(active.Name, profile.DefaultName))),
			buildPathCheck("home", active.HomeDir, true, true),
			buildPathCheck("config", active.ConfigPath, false, true),
			buildPathCheck("env", active.EnvPath, false, true),
			buildPathCheck("runtime", active.RuntimePath, false, true),
		},
	}
}

func buildPathCheck(name string, path string, wantDir bool, optional bool) Check {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		status := StatusFail
		if optional {
			status = StatusWarn
		}
		return check(name, status, "path is not set")
	}

	info, err := statPath(path)
	if err != nil {
		if os.IsNotExist(err) {
			status := StatusFail
			message := fmt.Sprintf("%q does not exist", path)
			if optional {
				status = StatusWarn
				message = fmt.Sprintf("%q is not present", path)
			}
			return check(name, status, message)
		}

		return check(name, StatusFail, err.Error())
	}
	if wantDir && !info.IsDir() {
		return check(name, StatusFail, fmt.Sprintf("%q is not a directory", path))
	}
	if !wantDir && info.IsDir() {
		return check(name, StatusFail, fmt.Sprintf("%q is a directory", path))
	}

	return check(name, StatusPass, fmt.Sprintf("found %q", path))
}

func defaultString(value string, fallback string) string {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return fallback
	}

	return valueText
}
