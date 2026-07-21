# Hướng dẫn Patch Teleport OSS để hỗ trợ Google SSO (OIDC)

## Mục tiêu
- Bật **OIDC connector** trong bản Community (OSS) để login bằng **Google Workspace** (và các IdP OIDC khác).
- OSS chỉ hỗ trợ GitHub connector native; OIDC/SAML là tính năng Enterprise.
- Thay đổi gọn, gói thành **git patch** để dễ merge/rebase khi Teleport ra version mới.

## Quan trọng: thực tế khác với giả định ban đầu

Bản hướng dẫn cũ giả định "chỉ cần comment dòng error `errOIDCNotImplemented`" là đủ. **Điều này sai.**
Từ commit `7b4c99b491` (#18999), toàn bộ logic OIDC đã bị **gỡ khỏi OSS** và chuyển vào
submodule enterprise `e/` (private, rỗng trong bản OSS). Hiện trạng:

- `lib/auth/oidc.go` chỉ còn là **lớp vỏ delegate**; nó gọi `a.oidcAuthService` (một interface
  `OIDCService`). Nếu service này `nil` thì trả `errOIDCNotImplemented`.
- `SetOIDCService` **không được gọi ở đâu trong OSS** (chỉ trong test) ⇒ `oidcAuthService` luôn `nil`.
- Việc chặn nằm ở nhiều lớp: entitlement (`auth_with_roles.go`), default Features (`modules.go`),
  và route web login/callback + endpoint validate của auth server **chưa được đăng ký** trong OSS.

Kết luận: phải **tự viết một implementation của `OIDCService`** rồi mở các gate, chứ không chỉ
comment một dòng.

## Nguyên tắc thiết kế
- Chỉ sửa chỗ gating enterprise + thêm một file implementation mới; không đụng logic GitHub, không sửa React UI.
- Dùng thư viện đã có sẵn trong `go.mod`: `golang.org/x/oauth2` và `github.com/coreos/go-oidc/v3`.
- Mirror theo luồng GitHub connector (`lib/auth/github.go`) để dễ bảo trì qua các version.
- Mỗi thay đổi đều có comment `// PATCH: enable OIDC for OSS` để dễ tìm khi rebase.

## Bước 1: Chuẩn bị repo

```bash
git clone https://github.com/gravitational/teleport.git
cd teleport
git checkout <tag-muốn-dùng>          # ví dụ v18.x hoặc tag mới nhất
git checkout -b patch/google-sso-oss
```

## Bước 2: Các thay đổi cần thực hiện

### 2.1. `lib/auth/oidc_oss.go` (file MỚI — phần lõi)
Thêm một implementation OSS của interface `OIDCService`:

- `struct ossOIDCService` giữ back-reference `*Server` và `emitter`.
- `CreateOIDCAuthRequest`: resolve connector; discovery provider bằng `oidc.NewProvider(ctx, issuerURL)`;
  dựng `oauth2.Config{ClientID, ClientSecret, RedirectURL, Endpoint: provider.Endpoint(), Scopes}`
  (scope mặc định `openid`, `email` + scope trong connector); sinh `StateToken`; gán
  `RedirectURL = config.AuthCodeURL(state, ...)` (kèm `prompt`/`acr_values` nếu có); lưu request.
- `ValidateOIDCAuthCallback`: đổi `code` lấy token (`config.Exchange`); verify `id_token` bằng
  `provider.Verifier(...).Verify(...)`; đọc claims; merge thêm claims từ `provider.UserInfo(...)`
  (kèm chống token-substitution qua so khớp `sub`); kiểm tra `email_verified`; map claims→traits→roles
  qua `services.TraitsToRoles(connector.GetTraitMappings(), traits)`; chạy login rules; tạo/cập nhật user
  (set `OIDCIdentities`); phát audit event; tạo web session/cert giống GitHub.
- `CreateOIDCAuthRequestForMFA`: trả `trace.NotImplemented(...)` (SSO MFA chưa hỗ trợ trong bản OSS này).

### 2.2. `lib/auth/auth.go` — đăng ký service
Cuối `NewServer`, trước `return as, nil`:

```go
// PATCH: enable OIDC for OSS
if as.oidcAuthService == nil {
    as.SetOIDCService(newOSSOIDCService(as))
}
```

(Enterprise sẽ đăng ký service riêng qua plugin registry sau đó và override — không xung đột.)

### 2.3. `lib/modules/modules.go` — mở entitlement
Thêm vào map `Entitlements` trong `defaultModules.Features()`:

```go
// PATCH: enable OIDC for OSS
entitlements.OIDC: {Enabled: true, Limit: 0},
```

Chỉ cần thay đổi này là 4 check `GetEntitlement(entitlements.OIDC).Enabled` trong
`lib/auth/auth_with_roles.go` (Upsert/Update/Create connector và CreateOIDCAuthRequest) đều tự pass.

### 2.4. `lib/auth/apiserver.go` — endpoint validate của auth server
Đăng ký route (đang chỉ do enterprise đăng ký):

```go
// PATCH: enable OIDC for OSS
srv.POST("/:version/oidc/requests/validate", srv.WithAuth(srv.validateOIDCAuthCallback))
```

và thêm handler `validateOIDCAuthCallback` (mirror `validateGithubAuthCallback`, trả
`authclient.OIDCAuthRawResponse`).

### 2.5. `lib/web/apiserver.go` — route + handler web login/callback
Đăng ký cạnh route GitHub:

```go
// PATCH: enable OIDC for OSS
h.GET("/webapi/oidc/login/web", h.WithRedirect(h.oidcLoginWeb))
h.GET("/webapi/oidc/callback", h.WithMetaRedirect(h.oidcCallback))
h.POST("/webapi/oidc/login/console", h.WithLimiter(h.oidcLoginConsole))
```

và thêm 3 handler `oidcLoginWeb` / `oidcLoginConsole` / `oidcCallback` (mirror handler GitHub tương ứng).
Nút "Login with Google" trên UI tự hiển thị theo `display` của connector, không cần sửa frontend.

### 2.6. `go.mod`
Đảm bảo `github.com/coreos/go-oidc/v3` là dependency **trực tiếp** (chạy `go mod tidy` hoặc build với
`-mod=mod` sẽ tự chuyển).

## Bước 3: Build & tạo patch

```bash
go mod tidy
make build            # build trên Linux (cần CGO/pkcs11 toolchain)

git add -A
git diff --cached -- lib go.mod > patch-google-sso-oss.patch
git commit -m "feat(oss): enable OIDC connector for Google SSO"
```

Lưu `patch-google-sso-oss.patch` cùng file guide này.

## Bước 4: Cấu hình & test

Tạo OAuth 2.0 Client ID (Web application) trong Google Cloud Console; Authorized redirect URI phải là
`https://<proxy>/v1/webapi/oidc/callback`.

**`google-oidc.yaml`**:

```yaml
kind: oidc
version: v3
metadata:
  name: google
spec:
  client_id: <your-google-client-id>
  client_secret: <your-google-client-secret>
  issuer_url: https://accounts.google.com
  redirect_url: https://your-teleport.example.com/v1/webapi/oidc/callback
  display: Google
  scope: [openid, email, profile]
  claims_to_roles:
    - claim: email
      value: "*"
      roles: [access]
```

```bash
tctl create -f google-oidc.yaml
```

Set auth preference (`cluster_auth_preference`, `type: oidc`, `connector_name: google`) rồi thử
login web và `tsh login --auth=google`.

## Bước 5: Cập nhật Teleport version mới

```bash
git fetch --tags
git checkout vNEW.VERSION
git apply patch-google-sso-oss.patch   # sửa conflict nhỏ nếu có → commit → tạo patch mới
```

## Lưu ý
- **License**: bật tính năng Enterprise trên bản OSS có thể vi phạm license Teleport Enterprise.
  Chỉ dùng cho self-hosted nội bộ.
- Đây là thay đổi lớn hơn "patch tối thiểu" (khoảng vài trăm dòng), vì phải viết lại logic OIDC.
- Test kỹ login flow, callback, claims→roles trước khi dùng production.

## Tham khảo
- Luồng GitHub connector native: `lib/auth/github.go`
- Ví dụ connector: `examples/resources/oidc-connector.yaml`
- Google OIDC: https://developers.google.com/identity/openid-connect/openid-connect
