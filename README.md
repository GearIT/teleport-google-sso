# Teleport Community Edition — bản sửa bật Google SSO (OIDC)

Đây là **bản fork đã chỉnh sửa** của [Teleport Community Edition](https://github.com/gravitational/teleport)
nhằm **bật đăng nhập Google SSO qua OIDC** trên bản OSS (mặc định OIDC là tính năng
của Teleport Enterprise). README gốc của Teleport nằm ở [`README-origin.md`](./README-origin.md).

## ⚠️ Disclaimer

- Đây là bản build **không chính thức**, **KHÔNG liên kết với, KHÔNG được tài trợ
  hay chứng thực** bởi Gravitational, Inc. / Teleport.
- "Teleport" là thương hiệu của Gravitational, Inc. Tên đó chỉ được dùng ở đây để
  mô tả phần mềm gốc mà bản này bắt nguồn.
- Phần mềm cung cấp **"nguyên trạng" (AS IS)**, không bảo hành. Tự chịu rủi ro khi dùng.
- Tính năng OIDC ở đây là mã **tự viết lại** cho bản OSS, **không** sao chép mã nguồn
  Teleport Enterprise.

## Thay đổi so với bản gốc (AGPL §5 – change notice)

- `lib/auth/oidc_oss.go`: triển khai OIDCService thuần OSS (authz code flow, xác minh
  id_token, gộp UserInfo, kiểm tra `email_verified`, ánh xạ claims→roles, tạo user & session).
- `lib/auth/auth.go`: đăng ký OSS OIDC service khi bản Enterprise vắng mặt.
- `lib/modules/modules.go`: bật entitlement OIDC cho bản OSS.
- `lib/auth/apiserver.go`, `lib/web/apiserver.go`: đăng ký các route OIDC còn thiếu.
- `deploy/`, `.github/workflows/release.yml`: Dockerfile runtime, docker-compose,
  mẫu cấu hình và pipeline build/release.

## NOTICE / Giấy phép

- Toàn bộ mã trong repo (trừ `/api`) theo **GNU AGPL-3.0** — xem [`LICENSE`](./LICENSE).
- Thư mục `/api` theo **Apache 2.0** — xem [`api/LICENSE`](./api/LICENSE).
- Bản sửa này **giữ nguyên AGPL-3.0**. Vì đây là phần mềm phục vụ qua mạng, theo AGPL
  §13 mã nguồn bản sửa được công khai tại chính repo này.
- Bản quyền phần gốc thuộc về Gravitational, Inc. và các cộng tác viên Teleport.

## Build

Binaries + Docker image được build tự động bởi
[`.github/workflows/release.yml`](./.github/workflows/release.yml): khi push tag `v*`,
workflow build một lần trong buildbox của Teleport (đã nhúng web UI), rồi đẩy image
lên GHCR và đính kèm binaries vào GitHub Release.

Build image tại chỗ (sau khi đã có binaries trong `build/`):

```bash
docker build -f deploy/Dockerfile -t teleport-oidc:local .
```

## [Quick Start](/deploy/README.md)

## Triển khai cơ bản (docker-compose)

Trên server, dùng thư mục [`deploy/`](./deploy):

1. Sửa `deploy/teleport.yaml`: đặt `public_addr`, `cluster_name`, email ACME thật.
2. Tạo connector Google từ mẫu và điền credentials:

```bash
cp deploy/google-oidc.example.yaml deploy/google-oidc.yaml
# điền client_id / client_secret (Google Cloud Console → OAuth 2.0 Client ID, type Web application)
# đặt redirect_url = https://<domain>/v1/webapi/oidc/callback (khớp y hệt bên Google)
```

3. Kéo image và chạy:

```bash
cd deploy
docker compose pull
docker compose up -d
```

4. Tạo admin và nạp cấu hình OIDC:

```bash
docker compose exec teleport tctl users add admin --roles=editor,access,auditor
docker compose exec teleport tctl create -f /etc/teleport/google-oidc.yaml
docker compose exec teleport tctl create -f /etc/teleport/cluster-auth-preference.yaml
```

Sau đó vào `https://<domain>` sẽ thấy nút đăng nhập **Google**.
