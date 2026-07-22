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

package defaults

// DefaultAGPLCDNBaseURL is where AGPL-built Teleport binaries are published for
// this fork. Install/join scripts append the artifact name, e.g.
// teleport-v19.0.0-linux-amd64-bin.tar.gz
//
// GitHub latest-asset pattern: /releases/latest/download/<filename>
// Override at runtime with TELEPORT_CDN_BASE_URL if needed.
const DefaultAGPLCDNBaseURL = "https://github.com/GearIT/teleport-google-sso/releases/latest/download"
