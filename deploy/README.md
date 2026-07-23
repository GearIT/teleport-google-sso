# Quick start
## 1. Cài đặt
- [Sử dụng bộ cài mặc định của bản community để cài đặt nhanh](https://goteleport.com/docs/get-started/deploy-community/)

### Install
```
curl https://cdn.teleport.dev/install.sh | bash -s 18.10.0
```

### Configure
- On your Teleport host, place a valid private key and a certificate chain in /var/lib/teleport/privkey.pem and /var/lib/teleport/fullchain.pem respectively.
```
sudo mkdir -p /var/lib/teleport
cd /var/lib/teleport

sudo openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
  -keyout /var/lib/teleport/privkey.pem \
  -out /var/lib/teleport/fullchain.pem \
  -subj "/C=VN/ST=Hanoi/L=Hanoi/O=MyCompany/OU=IT/CN=teleport.example.com" \
  -addext "subjectAltName = DNS:teleport.example.com,IP:192.168.x.x"

sudo chmod 600 privkey.pem fullchain.pem

```
- Teleport Configure
```
sudo teleport configure -o file \
    --cluster-name=teleport.example.com \
    --public-addr=teleport.example.com:443 \
    --cert-file=/var/lib/teleport/fullchain.pem \
    --key-file=/var/lib/teleport/privkey.pem
```

### Patch AGPL version

- chạy đoạn command sau để chuyển sang bản AGPL

```
cd /tmp
curl -LO https://github.com/GearIT/teleport-google-sso/releases/download/v19.0.0-oidc.6/teleport-v19.0.0-prealpha.2-linux-amd64-bin.tar.gz
tar xzf teleport-v19.0.0-prealpha.2-linux-amd64-bin.tar.gz
sudo systemctl stop teleport
sudo install -m0755 teleport/teleport /opt/teleport/system/bin/teleport
sudo install -m0755 teleport/tctl     /opt/teleport/system/bin/tctl
sudo install -m0755 teleport/tsh      /opt/teleport/system/bin/tsh
sudo install -m0755 teleport/tbot     /opt/teleport/system/bin/tbot
teleport version
sudo systemctl start teleport
```

- [hoặc dùng script được viết sẵn trong repo này](/deploy/update-teleport.sh)

```
cd $HOME/teleport-google-sso/deploy
bash update-teleport.sh
```

### Start service
```
sudo systemctl enable teleport
sudo systemctl start teleport
```

### [Bonus thêm script tháo cài đặt teleport ở máy client nếu lỡ cài bản community](/deploy/remove-teleport-client.sh)

## 2. Tạo user admin quản lý

- Chạy command sau để tạo
```
sudo tctl users add teleport-admin --roles=editor,access,auditor --logins=root,ubuntu,ec2-user
```
- Command sẽ in ra địa chỉ truy cập tạm thời để tạo tài khoản admin
```
User "teleport-admin" has been created but requires a password. Share this URL with the user to complete user setup, link is valid for 1h:
https://teleport.example.com:443/web/invite/123abc456def789ghi123abc456def78

NOTE: Make sure teleport.example.com:443 points at a Teleport proxy which users can access.
```

## 3. Cấu hình nâng cao
### Cấu hình Google OIDC
- sửa lại file cấu hình Google và chạy lệnh thực thi
```
cp $HOME/teleport-google-sso/deploy/google-oidc.example.yaml $HOME/google-oidc.example.yaml

nano $HOME/google-oidc.example.yaml

# sửa lại cấu hình cần thiết sau đó chạy lệnh thực thi
sudo tctl create -f $HOME/google-oidc.yaml
cp $HOME/teleport-google-sso/deploy/cluster-auth-preference.yaml $HOME/cluster-auth-preference.yaml
sudo tctl create -f $HOME/cluster-auth-preference.yaml
```

