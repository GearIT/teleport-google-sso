/*
 * Teleport
 * Copyright (C) 2023  Gravitational, Inc.
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

// PATCH: enable OIDC for OSS
//
// This file provides a native, self-contained OIDCService implementation for
// the open source build. Upstream moved the OIDC connector login logic out of
// OSS (commit 7b4c99b491, #18999) and into the enterprise plugin, leaving
// lib/auth/oidc.go as a thin delegating wrapper whose oidcAuthService is only
// registered by enterprise code. This implementation restores a working OIDC
// login flow for OSS using the maintained coreos/go-oidc/v3 and x/oauth2
// libraries, mirroring the native GitHub connector flow in lib/auth/github.go.
//
// It is registered via Server.SetOIDCService during auth server initialization
// (see lib/service/service.go).

package auth

import (
	"context"
	"net/url"

	oidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/gravitational/trace"
	"golang.org/x/oauth2"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/constants"
	apidefaults "github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	apievents "github.com/gravitational/teleport/api/types/events"
	apiutils "github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/api/utils/keys/hardwarekey"
	"github.com/gravitational/teleport/lib/auth/authclient"
	"github.com/gravitational/teleport/lib/authz"
	"github.com/gravitational/teleport/lib/client/sso"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/loginrule"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/utils"
)

// ErrOIDCNoRoles results from not mapping any roles from OIDC claims.
var ErrOIDCNoRoles = trace.AccessDenied("No roles mapped from claims. The mappings may contain typos.")

// ossOIDCService is a native OSS implementation of the OIDCService interface
// (see lib/auth/oidc.go). It performs the standard OIDC authorization code flow
// against the provider configured in the OIDC connector.
type ossOIDCService struct {
	a       *Server
	emitter apievents.Emitter
}

// newOSSOIDCService returns an OIDCService backed by the given auth server.
func newOSSOIDCService(a *Server) *ossOIDCService {
	return &ossOIDCService{
		a:       a,
		emitter: a.emitter,
	}
}

var _ OIDCService = (*ossOIDCService)(nil)

// CreateOIDCAuthRequest creates a new OIDC authorization code request and
// returns it with the provider redirect URL populated.
func (s *ossOIDCService) CreateOIDCAuthRequest(ctx context.Context, req types.OIDCAuthRequest) (*types.OIDCAuthRequest, error) {
	connector, err := s.getOIDCConnector(ctx, req)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// Requests for a web session originate from the proxy and are trusted;
	// requests for a client session (tsh login) need their client redirect URL
	// validated, as they point the browser away from the IdP after auth.
	if !req.CreateWebSession {
		ceremonyType := sso.CeremonyTypeLogin
		if req.SSOTestFlow {
			ceremonyType = sso.CeremonyTypeTest
		}
		if err := sso.ValidateClientRedirect(req.ClientRedirectURL, ceremonyType, connector.GetClientRedirectSettings()); err != nil {
			return nil, trace.Wrap(err, InvalidClientRedirectErrorMessage)
		}
	}

	oauthConfig, _, err := s.oauth2ConfigForConnector(ctx, connector, req.ProxyAddress)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	req.StateToken, err = utils.CryptoRandomHex(defaults.TokenLenBytes)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOnline}
	if prompt := connector.GetPrompt(); prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	if acr := connector.GetACR(); acr != "" {
		opts = append(opts, oauth2.SetAuthURLParam("acr_values", acr))
	}
	req.RedirectURL = oauthConfig.AuthCodeURL(req.StateToken, opts...)

	s.a.logger.DebugContext(ctx, "Creating OIDC auth request", "redirect_url", req.RedirectURL)

	if err := s.a.Services.CreateOIDCAuthRequest(ctx, req, defaults.OIDCAuthRequestTTL); err != nil {
		return nil, trace.Wrap(err)
	}
	return &req, nil
}

// CreateOIDCAuthRequestForMFA is not implemented in the OSS OIDC service.
func (s *ossOIDCService) CreateOIDCAuthRequestForMFA(ctx context.Context, req types.OIDCAuthRequest) (*types.OIDCAuthRequest, error) {
	return nil, trace.NotImplemented("OIDC SSO MFA is not supported by the open source OIDC service")
}

// ValidateOIDCAuthCallback validates the OIDC provider callback, emits audit
// events and returns the login response (session/certs).
func (s *ossOIDCService) ValidateOIDCAuthCallback(ctx context.Context, q url.Values) (*authclient.OIDCAuthResponse, error) {
	diagCtx := NewSSODiagContext(types.KindOIDC, s.a)

	event := &apievents.UserLogin{
		Metadata: apievents.Metadata{
			Type: events.UserLoginEvent,
		},
		Method:             events.LoginMethodOIDC,
		ConnectionMetadata: authz.ConnectionMetadata(ctx),
	}

	resp, err := s.validateOIDCAuthCallback(ctx, diagCtx, q)
	diagCtx.Info.Error = trace.UserMessage(err)
	event.AppliedLoginRules = diagCtx.Info.AppliedLoginRules

	diagCtx.WriteToBackend(ctx)

	if claims := diagCtx.Info.OIDCClaims; claims != nil {
		attributes, encErr := apievents.EncodeMap(claims)
		if encErr != nil {
			s.a.logger.DebugContext(ctx, "Failed to encode OIDC identity attributes", "error", encErr)
		} else {
			event.IdentityAttributes = attributes
		}
	}

	if err != nil {
		event.Code = events.UserSSOLoginFailureCode
		if diagCtx.Info.TestFlow {
			event.Code = events.UserSSOTestFlowLoginFailureCode
		}
		event.Status.Success = false
		event.Status.Error = trace.Unwrap(err).Error()
		event.Status.UserMessage = err.Error()

		if emitErr := s.emitter.EmitAuditEvent(ctx, event); emitErr != nil {
			s.a.logger.WarnContext(ctx, "Failed to emit OIDC login failed event", "error", emitErr)
		}
		return nil, trace.Wrap(err)
	}

	event.Code = events.UserSSOLoginCode
	if diagCtx.Info.TestFlow {
		event.Code = events.UserSSOTestFlowLoginCode
	}
	event.Status.Success = true
	event.User = resp.Username

	if emitErr := s.emitter.EmitAuditEvent(ctx, event); emitErr != nil {
		s.a.logger.WarnContext(ctx, "Failed to emit OIDC login event", "error", emitErr)
	}

	return resp, nil
}

func (s *ossOIDCService) validateOIDCAuthCallback(ctx context.Context, diagCtx *SSODiagContext, q url.Values) (*authclient.OIDCAuthResponse, error) {
	if errParam := q.Get("error"); errParam != "" {
		// Try to find the request so the error gets logged against it.
		if state := q.Get("state"); state != "" {
			diagCtx.RequestID = state
			if req, err := s.a.GetOIDCAuthRequest(ctx, state); err == nil {
				diagCtx.Info.TestFlow = req.SSOTestFlow
			}
		}
		errDesc := q.Get("error_description")
		oauthErr := trace.OAuth2("invalid_request", errParam, q)
		return nil, trace.WithUserMessage(oauthErr, "OIDC provider returned error: %v [%v]", errDesc, errParam)
	}

	code := q.Get("code")
	if code == "" {
		oauthErr := trace.OAuth2("invalid_request", "code query param must be set", q)
		return nil, trace.WithUserMessage(oauthErr, "Invalid parameters received from OIDC provider.")
	}

	stateToken := q.Get("state")
	if stateToken == "" {
		oauthErr := trace.OAuth2("invalid_request", "missing state query param", q)
		return nil, trace.WithUserMessage(oauthErr, "Invalid parameters received from OIDC provider.")
	}
	diagCtx.RequestID = stateToken

	req, err := s.a.GetOIDCAuthRequest(ctx, stateToken)
	if err != nil {
		return nil, trace.Wrap(err, "Failed to get OIDC Auth Request.")
	}
	diagCtx.Info.TestFlow = req.SSOTestFlow

	connector, err := s.getOIDCConnector(ctx, *req)
	if err != nil {
		return nil, trace.Wrap(err, "Failed to get OIDC connector.")
	}

	oauthConfig, provider, err := s.oauth2ConfigForConnector(ctx, connector, req.ProxyAddress)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	claims, err := s.getClaims(ctx, connector, provider, oauthConfig, code)
	if err != nil {
		return nil, trace.Wrap(err, "Failed to extract OIDC claims. This may indicate a need to set the 'provider' flag in the connector definition.")
	}
	diagCtx.Info.OIDCClaims = types.OIDCClaims(claims)

	if !connector.GetAllowUnverifiedEmail() {
		if err := checkOIDCEmailVerifiedClaim(claims); err != nil {
			return nil, trace.Wrap(err, "OIDC provider did not verify email.")
		}
	}

	ident := oidcIdentityFromClaims(claims)
	diagCtx.Info.OIDCIdentity = ident

	if len(connector.GetClaimsToRoles()) == 0 {
		return nil, trace.BadParameter("no claims to roles mapping, check connector documentation")
	}
	diagCtx.Info.OIDCClaimsToRoles = connector.GetClaimsToRoles()

	params, err := s.calculateOIDCUser(ctx, diagCtx, connector, claims, ident, req)
	if err != nil {
		return nil, trace.Wrap(err, "Failed to calculate user attributes.")
	}

	diagCtx.Info.CreateUserParams = &types.CreateUserParams{
		ConnectorName: params.ConnectorName,
		Username:      params.Username,
		KubeGroups:    params.KubeGroups,
		KubeUsers:     params.KubeUsers,
		Roles:         params.Roles,
		Traits:        params.Traits,
		SessionTTL:    types.Duration(params.SessionTTL),
	}

	user, err := s.createOIDCUser(ctx, params, req.SSOTestFlow)
	if err != nil {
		return nil, trace.Wrap(err, "Failed to create user from provided parameters.")
	}

	if err := s.a.CallLoginHooks(ctx, user); err != nil {
		return nil, trace.Wrap(err)
	}

	// In test flow skip signing and creating web sessions.
	if req.SSOTestFlow {
		diagCtx.Info.Success = true
		return &authclient.OIDCAuthResponse{
			Req:      oidcAuthRequestFromProto(req),
			Identity: types.ExternalIdentity{ConnectorID: params.ConnectorName, Username: params.Username},
			Username: user.GetName(),
		}, nil
	}

	userState, err := s.a.GetUserOrLoginState(ctx, user.GetName())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return s.makeOIDCAuthResponse(ctx, req, userState, params)
}

func (s *ossOIDCService) makeOIDCAuthResponse(ctx context.Context, req *types.OIDCAuthRequest, userState services.UserState, params *CreateUserParams) (*authclient.OIDCAuthResponse, error) {
	a := s.a
	auth := authclient.OIDCAuthResponse{
		Req:      oidcAuthRequestFromProto(req),
		Identity: types.ExternalIdentity{ConnectorID: params.ConnectorName, Username: params.Username},
		Username: userState.GetName(),
	}

	// If the request is coming from a browser, create a web session.
	if req.CreateWebSession {
		session, err := a.CreateWebSessionFromReq(ctx, NewWebSessionRequest{
			User:                 userState.GetName(),
			Roles:                userState.GetRoles(),
			Traits:               userState.GetTraits(),
			SessionTTL:           params.SessionTTL,
			LoginTime:            a.clock.Now().UTC(),
			LoginIP:              req.ClientLoginIP,
			LoginUserAgent:       req.ClientUserAgent,
			AttestWebSession:     true,
			CreateDeviceWebToken: true,
		})
		if err != nil {
			return nil, trace.Wrap(err, "Failed to create web session.")
		}
		auth.Session = session
	}

	// If a public key was provided, sign it and return a certificate.
	if len(req.SshPublicKey) != 0 || len(req.TlsPublicKey) != 0 {
		sshCert, tlsCert, err := a.CreateSessionCerts(ctx, &SessionCertsRequest{
			UserState:               userState,
			SessionTTL:              params.SessionTTL,
			SSHPubKey:               req.SshPublicKey,
			TLSPubKey:               req.TlsPublicKey,
			SSHAttestationStatement: hardwarekey.AttestationStatementFromProto(req.SshAttestationStatement),
			TLSAttestationStatement: hardwarekey.AttestationStatementFromProto(req.TlsAttestationStatement),
			Compatibility:           req.Compatibility,
			RouteToCluster:          req.RouteToCluster,
			KubernetesCluster:       req.KubernetesCluster,
			LoginIP:                 req.ClientLoginIP,
		})
		if err != nil {
			return nil, trace.Wrap(err, "Failed to create session certificate.")
		}

		clusterName, err := a.GetClusterName(ctx)
		if err != nil {
			return nil, trace.Wrap(err, "Failed to obtain cluster name.")
		}

		auth.Cert = sshCert
		auth.TLSCert = tlsCert

		authority, err := a.GetCertAuthority(ctx, types.CertAuthID{
			Type:       types.HostCA,
			DomainName: clusterName.GetClusterName(),
		}, false)
		if err != nil {
			return nil, trace.Wrap(err, "Failed to obtain cluster's host CA.")
		}
		auth.HostSigners = append(auth.HostSigners, authority)
	}

	if o, err := a.ClientOptionsForLogin(userState); err == nil {
		auth.ClientOptions = o
	} else {
		s.a.logger.WarnContext(ctx, "Failed to calculate client options for OIDC login", "username", userState.GetName(), "error", err)
	}

	return &auth, nil
}

// getOIDCConnector resolves the OIDC connector for the given request, handling
// the stateless SSO test flow.
func (s *ossOIDCService) getOIDCConnector(ctx context.Context, req types.OIDCAuthRequest) (types.OIDCConnector, error) {
	if req.SSOTestFlow {
		if req.ConnectorSpec == nil {
			return nil, trace.BadParameter("ConnectorSpec cannot be nil when SSOTestFlow is true")
		}
		if req.ConnectorID == "" {
			return nil, trace.BadParameter("ConnectorID cannot be empty")
		}
		connector, err := types.NewOIDCConnector(req.ConnectorID, *req.ConnectorSpec)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		return connector, nil
	}

	connector, err := s.a.GetOIDCConnector(ctx, req.ConnectorID, true)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return connector, nil
}

// oauth2ConfigForConnector builds an oauth2.Config and discovers the OIDC
// provider for the given connector.
func (s *ossOIDCService) oauth2ConfigForConnector(ctx context.Context, connector types.OIDCConnector, proxyAddr string) (*oauth2.Config, *oidc.Provider, error) {
	redirectURL, err := services.GetRedirectURL(connector, proxyAddr)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}

	provider, err := oidc.NewProvider(ctx, connector.GetIssuerURL())
	if err != nil {
		return nil, nil, trace.Wrap(err, "failed to query OIDC provider %q", connector.GetIssuerURL())
	}

	scopes := apiutils.Deduplicate(append([]string{oidc.ScopeOpenID, "email"}, connector.GetScope()...))
	config := &oauth2.Config{
		ClientID:     connector.GetClientID(),
		ClientSecret: connector.GetClientSecret(),
		RedirectURL:  redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}
	return config, provider, nil
}

// getClaims exchanges the authorization code for tokens, verifies the ID token
// and merges UserInfo claims into the ID token claims.
func (s *ossOIDCService) getClaims(ctx context.Context, connector types.OIDCConnector, provider *oidc.Provider, oauthConfig *oauth2.Config, code string) (map[string]any, error) {
	token, err := oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, trace.Wrap(err, "requesting OIDC OAuth2 token failed; the client_id and/or client_secret may be incorrect")
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, trace.BadParameter("no id_token found in the OAuth2 token response from the OIDC provider")
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: connector.GetClientID()})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, trace.Wrap(err, "failed to verify OIDC ID token")
	}

	idClaims := map[string]any{}
	if err := idToken.Claims(&idClaims); err != nil {
		return nil, trace.Wrap(err, "failed to extract claims from OIDC ID token")
	}

	// Merge claims from the UserInfo endpoint if available. Failures here are
	// non-fatal: we fall back to the verified ID token claims.
	userInfo, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		s.a.logger.DebugContext(ctx, "Unable to fetch OIDC UserInfo claims; using ID token claims only", "error", err)
		return idClaims, nil
	}

	uiClaims := map[string]any{}
	if err := userInfo.Claims(&uiClaims); err != nil {
		s.a.logger.DebugContext(ctx, "Unable to decode OIDC UserInfo claims; using ID token claims only", "error", err)
		return idClaims, nil
	}

	// Guard against token substitution: the subject in UserInfo must match the
	// subject in the ID token (OIDC spec section 16.11).
	idSub, _ := idClaims["sub"].(string)
	uiSub, _ := uiClaims["sub"].(string)
	if idSub != "" && uiSub != "" && idSub != uiSub {
		return nil, trace.BadParameter("OIDC claim subjects in UserInfo does not match ID token")
	}

	for k, v := range uiClaims {
		if _, exists := idClaims[k]; !exists {
			idClaims[k] = v
		}
	}
	return idClaims, nil
}

func (s *ossOIDCService) calculateOIDCUser(ctx context.Context, diagCtx *SSODiagContext, connector types.OIDCConnector, claims map[string]any, ident *types.OIDCIdentity, request *types.OIDCAuthRequest) (*CreateUserParams, error) {
	username, err := usernameFromOIDCClaims(connector, claims, ident)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	p := CreateUserParams{
		ConnectorName: connector.GetName(),
		Username:      username,
	}

	traits := oidcClaimsToTraits(claims)
	diagCtx.Info.OIDCTraitsFromClaims = traits
	diagCtx.Info.OIDCConnectorTraitMapping = connector.GetTraitMappings()

	evalInput := &loginrule.EvaluationInput{Traits: traits}
	evalOutput, err := s.a.GetLoginRuleEvaluator().Evaluate(ctx, evalInput)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	p.Traits = evalOutput.Traits
	diagCtx.Info.AppliedLoginRules = evalOutput.AppliedRules

	p.KubeGroups = p.Traits[constants.TraitKubeGroups]
	p.KubeUsers = p.Traits[constants.TraitKubeUsers]

	var warnings []string
	warnings, p.Roles = services.TraitsToRoles(connector.GetTraitMappings(), p.Traits)
	if len(p.Roles) == 0 {
		if len(warnings) > 0 {
			diagCtx.Info.OIDCClaimsToRolesWarnings = &types.SSOWarnings{
				Message:  "No roles mapped for the user",
				Warnings: warnings,
			}
		} else {
			diagCtx.Info.OIDCClaimsToRolesWarnings = &types.SSOWarnings{
				Message: "No roles mapped for the user. The mappings may contain typos.",
			}
		}
		return nil, trace.Wrap(ErrOIDCNoRoles)
	}

	// Pick smaller of role session TTL or requested TTL.
	roles, err := services.FetchRolesWithContext(p.Roles, s.a, services.RoleTemplateContext{
		Username: p.Username,
		Traits:   p.Traits,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	roleTTL := roles.AdjustSessionTTL(apidefaults.MaxCertDuration)
	p.SessionTTL = utils.MinTTL(roleTTL, request.CertTTL)

	return &p, nil
}

func (s *ossOIDCService) createOIDCUser(ctx context.Context, p *CreateUserParams, dryRun bool) (types.User, error) {
	a := s.a
	a.logger.DebugContext(ctx, "Generating dynamic OIDC identity",
		"connector_name", p.ConnectorName,
		"user_name", p.Username,
		"roles", p.Roles,
		"dry_run", dryRun,
	)

	expires := a.GetClock().Now().UTC().Add(p.SessionTTL)

	user := &types.UserV2{
		Kind:    types.KindUser,
		Version: types.V2,
		Metadata: types.Metadata{
			Name:      p.Username,
			Namespace: apidefaults.Namespace,
			Expires:   &expires,
		},
		Spec: types.UserSpecV2{
			Roles:  p.Roles,
			Traits: p.Traits,
			OIDCIdentities: []types.ExternalIdentity{{
				ConnectorID: p.ConnectorName,
				Username:    p.Username,
			}},
			CreatedBy: types.CreatedBy{
				User: types.UserRef{Name: teleport.UserSystem},
				Time: a.GetClock().Now().UTC(),
				Connector: &types.ConnectorRef{
					Type:     constants.OIDC,
					ID:       p.ConnectorName,
					Identity: p.Username,
				},
			},
		},
	}

	if dryRun {
		return user, nil
	}

	existingUser, err := a.Services.GetUser(ctx, p.Username, false)
	if err != nil && !trace.IsNotFound(err) {
		return nil, trace.Wrap(err)
	}

	if existingUser != nil {
		ref := user.GetCreatedBy().Connector
		if !ref.IsSameProvider(existingUser.GetCreatedBy().Connector) {
			return nil, trace.AlreadyExists("local user %q already exists and is not an OIDC user", existingUser.GetName())
		}

		user.SetRevision(existingUser.GetRevision())
		if _, err := a.UpdateUser(ctx, user); err != nil {
			return nil, trace.Wrap(err)
		}
	} else {
		if _, err := a.CreateUser(ctx, user); err != nil {
			return nil, trace.Wrap(err)
		}
	}

	return user, nil
}

// oidcAuthRequestFromProto converts a types.OIDCAuthRequest into the JSON-marshalable
// authclient.OIDCAuthRequest returned to the proxy.
func oidcAuthRequestFromProto(req *types.OIDCAuthRequest) authclient.OIDCAuthRequest {
	return authclient.OIDCAuthRequest{
		ConnectorID:       req.ConnectorID,
		CSRFToken:         req.CSRFToken,
		SSHPubKey:         req.SshPublicKey,
		TLSPubKey:         req.TlsPublicKey,
		CreateWebSession:  req.CreateWebSession,
		ClientRedirectURL: req.ClientRedirectURL,
	}
}

// usernameFromOIDCClaims resolves the Teleport username from the claims. If the
// connector sets username_claim, that claim is used; otherwise the email is used.
func usernameFromOIDCClaims(connector types.OIDCConnector, claims map[string]any, ident *types.OIDCIdentity) (string, error) {
	usernameClaim := connector.GetUsernameClaim()
	if usernameClaim == "" {
		if ident.Email == "" {
			return "", trace.BadParameter("no email claim received from the OIDC provider; set username_claim in connector %q", connector.GetName())
		}
		return ident.Email, nil
	}

	v, ok := claims[usernameClaim]
	if !ok {
		return "", trace.BadParameter("the configured username_claim of %q was not received from the IdP; update the username_claim in connector %q", usernameClaim, connector.GetName())
	}
	username, ok := v.(string)
	if !ok {
		return "", trace.BadParameter("the configured username_claim of %q is not a string", usernameClaim)
	}
	return username, nil
}

// oidcIdentityFromClaims extracts the OIDC identity fields from the claims.
func oidcIdentityFromClaims(claims map[string]any) *types.OIDCIdentity {
	id := &types.OIDCIdentity{}
	if v, ok := claims["sub"].(string); ok {
		id.ID = v
	}
	if v, ok := claims["email"].(string); ok {
		id.Email = v
	}
	if v, ok := claims["name"].(string); ok {
		id.Name = v
	}
	return id
}

// oidcClaimsToTraits converts OIDC-style claims into Teleport trait format.
func oidcClaimsToTraits(claims map[string]any) map[string][]string {
	traits := make(map[string][]string)
	for claimName, v := range claims {
		switch claimValue := v.(type) {
		case string:
			traits[claimName] = []string{claimValue}
		case []string:
			traits[claimName] = claimValue
		case []any:
			for _, vv := range claimValue {
				if s, ok := vv.(string); ok {
					traits[claimName] = append(traits[claimName], s)
				}
			}
		}
	}
	return traits
}

// checkOIDCEmailVerifiedClaim returns an error if the email_verified claim is
// present and indicates the email is not verified.
func checkOIDCEmailVerifiedClaim(claims map[string]any) error {
	const claimName = "email_verified"
	unverifiedErr := trace.AccessDenied("email not verified by OIDC provider")

	data, ok := claims[claimName]
	if !ok {
		return nil
	}

	switch v := data.(type) {
	case string:
		switch v {
		case "true":
			return nil
		case "false":
			return unverifiedErr
		default:
			return trace.BadParameter("unable to parse oidc claim %q, must be either 'true' or 'false', got %q", claimName, v)
		}
	case bool:
		if !v {
			return unverifiedErr
		}
		return nil
	default:
		return trace.BadParameter("unable to parse oidc claim %q, must be a string or bool", claimName)
	}
}
