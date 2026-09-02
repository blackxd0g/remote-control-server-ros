# RouterOS automated deployment

The scripts in `deploy/` target RouterOS 7.22 or newer on `linux/amd64` CHR. They follow the official MikroTik container model: a dedicated veth interface, persistent `/data` mount, Docker Hub registry, outbound NAT and narrowly scoped RustDesk-compatible protocol ports.

## Prerequisites

- install the RouterOS `container` package matching the router version;
- enable container mode with `/system/device-mode/update container=yes` and complete the physical confirmation/reboot required by RouterOS;
- ensure the router has enough storage and that the `WAN` interface list exists;
- back up the RouterOS configuration before importing any script.

## Install

1. Download `deploy/routeros-install.rsc`.
2. Edit the configuration block at the top. `publicHost` is mandatory. Change the network, storage path and WAN list if they overlap with the router configuration.
3. Leave `adminPassword` empty to generate first-run credentials in the persistent data directory. Do not commit a real password into the script.
4. Upload the file to RouterOS and validate it without applying changes:

```routeros
/import file-name=routeros-install.rsc verbose=yes dry-run=yes
```

5. Import it and wait for image extraction:

```routeros
/import file-name=routeros-install.rsc verbose=yes
/container/print detail where name="remote_control_server"
```

6. When the container is `stopped`, start it:

```routeros
/container/start [find where name="remote_control_server"]
```

The web/API port `21114` is intentionally not published to WAN by the script. Put it behind a trusted HTTPS reverse proxy. Protocol ports TCP `21115-21119` and UDP `21116` are forwarded from the configured WAN interface list.

## Update

Upload and import `deploy/routeros-update.rsc`. It invokes the official RouterOS container `update` command and keeps the existing veth, environment list and `/data` mount:

```routeros
/import file-name=routeros-update.rsc verbose=yes dry-run=yes
/import file-name=routeros-update.rsc verbose=yes
```

Pin `remote-image` to a numbered tag instead of `latest` when deterministic rollbacks are required. Always create or download a managed backup before an application upgrade.

## Russian quick reference

Скрипт установки создаёт отдельный veth, постоянный каталог `/data`, переменные окружения и правила только для необходимых протокольных портов. Перед импортом обязательно измените `publicHost`, проверьте отсутствие конфликта подсети `172.31.255.0/30` и выполните `dry-run`. Порт панели `21114` наружу не публикуется: подключайте его через HTTPS reverse proxy. Реальный пароль администратора не сохраняйте в файле, который отправляется в Git.
