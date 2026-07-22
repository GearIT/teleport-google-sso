/*
 * Teleport
 * Copyright (C) 2025  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package web

import (
	"os"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/modules"
	"github.com/gravitational/teleport/lib/web/scripts"
)

func TestGetCDNBaseURL(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, os.Unsetenv(EnvVarCDNBaseURL))
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv(EnvVarCDNBaseURL, "https://example.com/releases/latest/")
		url, err := getCDNBaseURL(modules.BuildOSS, teleport.SemVer())
		require.NoError(t, err)
		require.Equal(t, "https://example.com/releases/latest", url)
	})

	t.Run("agpl build defaults to github releases", func(t *testing.T) {
		url, err := getCDNBaseURL(modules.BuildOSS, teleport.SemVer())
		require.NoError(t, err)
		require.Equal(t, defaults.DefaultAGPLCDNBaseURL, url)
	})

	t.Run("community build uses official cdn", func(t *testing.T) {
		url, err := getCDNBaseURL(modules.BuildCommunity, semver.New("18.10.0"))
		require.NoError(t, err)
		require.Equal(t, "https://cdn.teleport.dev", url)
	})
}

func TestForceTarballInstall(t *testing.T) {
	require.False(t, scripts.ForceTarballInstall("https://cdn.teleport.dev"))
	require.False(t, scripts.ForceTarballInstall("https://cdn.teleport.dev/"))
	require.True(t, scripts.ForceTarballInstall(defaults.DefaultAGPLCDNBaseURL))
}
