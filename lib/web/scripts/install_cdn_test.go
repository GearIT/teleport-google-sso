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

package scripts

import (
	"context"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/defaults"
)

func TestGetNodeInstallScriptCustomCDN(t *testing.T) {
	script, err := GetNodeInstallScript(context.Background(), InstallNodeScriptOptions{
		InstallOptions: InstallScriptOptions{
			TeleportVersion: semver.New("19.0.0"),
			CDNBaseURL:      defaults.DefaultAGPLCDNBaseURL,
			TeleportFlavor:  types.PackageNameOSS,
			ProxyAddr:       "proxy.example.com:443",
		},
		Token:      "abc123",
		JoinMethod: types.JoinMethodToken,
	})
	require.NoError(t, err)
	require.Contains(t, script, "TELEPORT_CDN_BASE='"+defaults.DefaultAGPLCDNBaseURL+"'")
	require.Contains(t, script, "FORCE_TARBALL_INSTALL='true'")
	require.Contains(t, script, "${TELEPORT_CDN_BASE}/${TELEPORT_PACKAGE_NAME}-v${TELEPORT_VERSION}")
	require.NotContains(t, script, "https://cdn.teleport.dev/")
}
