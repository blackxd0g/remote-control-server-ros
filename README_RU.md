[English](README.md) | **Русский**

# Remote Control Server

> Лёгкая self-hosted платформа удалённого управления, совместимая с протоколом клиента RustDesk.
> Разработана для `linux/amd64`, MikroTik CHR и контейнеров RouterOS: аутентификация,
> контроль доступа, relay, API и современная web-консоль в одном решении.

[![Docker Image](https://img.shields.io/badge/Docker%20Hub-blackxdog%2Fremote--control--server--ros-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/blackxdog/remote-control-server-ros)
![Версия](https://img.shields.io/badge/version-2.0.1-0A84FF)
![Платформа](https://img.shields.io/badge/platform-linux%2Famd64-success)
![RouterOS](https://img.shields.io/badge/RouterOS-7.22%2B-blue)
![Протокол](https://img.shields.io/badge/protocol-RustDesk%20compatible-7B61FF)

## ✨ Возможности

- 🔐 Обязательный вход клиента до разрешения PunchHole или relay-соединения.
- 🎫 Argon2id, серверные сессии, JWT, logout, revoke и принудительный повторный вход.
- 👥 Пользователи, собственные RBAC-роли, группы и процесс одобрения регистрации.
- 🖥 Инвентаризация устройств, группы, теги, владельцы, online-статус и deployment control.
- 🛡 Проверка ACL до соединения: управление, просмотр, файлы, clipboard, tunnel и terminal.
- 🧭 Персональные и общие адресные книги, совместимые с актуальными маршрутами клиента.
- ⚙️ Наследуемые стратегии для global, user, group, device и device group.
- 📜 Аудит входов, подключений, отказов, изменений безопасности и передачи файлов.
- 🔑 TOTP, generic OIDC и шифрованная интеграция LDAP / Active Directory.
- 🌐 Несколько relay-серверов, health monitoring, телеметрия и выбор по нагрузке.
- 🔔 Подписанные webhooks, постоянные уведомления, резервные копии и автоматизация.
- 🧰 Профили управляемых клиентов и изолированный API для builder-worker.
- 📊 Адаптивная Vue 3 консоль с CPU, RAM, uptime, peers, sessions и relay metrics.
- 📦 Раздельные scratch-образы и компактный all-in-one образ для RouterOS.

> [!NOTE]
> Remote Control Server — независимый clean-room проект, не связанный с RustDesk.
> RustDesk является товарным знаком соответствующего правообладателя; название
> используется исключительно для обозначения совместимости протокола.

## ⚡ Быстрый старт

1. Укажите публичный домен и запустите all-in-one deployment:

```sh
export RDS_PUBLIC_HOST=remote.example.net
docker compose -f deploy/compose.all-in-one.yaml up -d
```

2. Прочитайте созданные при первом запуске реквизиты администратора:

```sh
docker compose -f deploy/compose.all-in-one.yaml exec rustdesk-server-routeros \
  cat /data/secrets/bootstrap-admin.txt
```

3. Для локальной проверки откройте `http://localhost:21114/`. В production
   публикуйте порт `21114` только через доверенный HTTPS reverse proxy.

Настройки клиента:

| Поле клиента | Значение |
|---|---|
| ID server | `remote.example.net:21116` |
| Relay server | `remote.example.net:21117` |
| API server | `https://remote.example.net` |
| Key | содержимое `/data/secrets/id_ed25519.pub` |

> [!IMPORTANT]
> После первого успешного входа администратора удалите `bootstrap-admin.txt`.
> Каталог `/data` должен быть постоянным: в нём находятся БД, ключи идентификации и секреты.

## 🚀 Автоматическая установка RouterOS

[`deploy/routeros-install.rsc`](deploy/routeros-install.rsc) подготавливает полный
deployment контейнера RouterOS. Перед импортом проверьте переменные в начале файла:

```routeros
:local publicHost "remote.example.net"
:local dataRoot "Containers/remote-control-server"
:local adminUsername "admin"
:local adminPassword ""
```

Пустой `adminPassword` безопасен: сервер создаст случайный bootstrap-пароль в
постоянном каталоге данных. Загрузите скрипт на роутер и выполните:

```routeros
/import file-name=routeros-install.rsc verbose=yes
```

Когда распаковка образа завершится со статусом `stopped`, запустите контейнер
командой, которую выведет скрипт. Для обновлений используйте
[`deploy/routeros-update.rsc`](deploy/routeros-update.rsc).

> [!IMPORTANT]
> До импорта проверьте `dataRoot`, сеть `172.31.255.0/30`, interface list `WAN`
> и действующие правила firewall.

## 🔌 Сетевые порты

| Порт | Протокол | Назначение |
|---:|---|---|
| `21114` | TCP | API и web-консоль; рекомендуется reverse proxy |
| `21115` | TCP | Проверка типа NAT |
| `21116` | TCP + UDP | Rendezvous / ID server |
| `21117` | TCP | Relay server |
| `21118` | TCP | WebSocket rendezvous |
| `21119` | TCP | WebSocket relay |

## 🧱 Архитектура

```text
Официальный клиент
    │ login / session token
    ▼
API Server ── users · sessions · devices · policies · audit
    │ события + периодическая сверка
    ▼
HBBS ── authentication ── ACL ── Strategy ── target lookup
    │
    ├── прямое P2P
    └── HBBR relay
```

| Компонент | Runtime | Назначение |
|---|---|---|
| `art-hbbs` | Rust | Rendezvous, совместимость протокола, auth/ACL/strategy gate |
| `art-hbbr` | Rust | Relay transport и телеметрия |
| `art-core` | Rust | Протокол, claims, cache, sync и общая security-логика |
| `art-api` | Go | API, persistence, auth, policies, audit и встроенные web-assets |
| `art-web` | Vue 3 + TypeScript | Статическая административная консоль |

## 💾 Постоянные данные

All-in-one образу требуется одна постоянная точка монтирования:

```text
RouterOS / Docker volume  →  /data
```

SQLite используется по умолчанию в лёгком режиме. Для крупных установок доступен
PostgreSQL. Общие JWT- и identity-секреты создаются один раз и сохраняются при перезапусках.

## 🛡 Безопасность

- Оставляйте `RDS_REQUIRE_LOGIN=true` в production.
- Завершайте публичный API-трафик через HTTPS и явно задавайте `RDS_TRUSTED_PROXIES`.
- Не публикуйте `/data`, файлы БД, приватные ключи, bootstrap credentials и секреты backup.
- Для PostgreSQL используйте отдельную учётную запись БД.
- Ограничивайте builder tokens изолированными worker-машинами; builder не получает секреты сервера и БД.
- Проверяйте аудит после изменений authentication, ACL, Strategies и конфигурации сервера.

## 🧪 Локальные проверки

```sh
(cd art-api && go vet ./... && go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...)

cargo fmt --all -- --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo build --workspace --release --target x86_64-unknown-linux-musl

(cd art-web && pnpm exec vue-tsc --noEmit && pnpm build)
```

## 📚 Документация

| Документ | Назначение |
|---|---|
| [`docs/routeros-autodeploy.md`](docs/routeros-autodeploy.md) | Установка и обновление RouterOS |
| [`docs/deployment.md`](docs/deployment.md) | Docker и production deployment |
| [`docs/architecture.md`](docs/architecture.md) | Границы сервисов и потоки данных |
| [`docs/security.md`](docs/security.md) | Модель безопасности и эксплуатация |
| [`docs/protocol-compatibility.md`](docs/protocol-compatibility.md) | Совместимость официального клиента |
| [`docs/upgrade-2.0.md`](docs/upgrade-2.0.md) | Обновление до версии 2.0 |
| [`docs/roadmap-status.md`](docs/roadmap-status.md) | Статус дорожной карты |

## 💖 Поддержка проекта

Если Remote Control Server оказался полезен и сэкономил вам время, вы можете
[поддержать разработку через Boosty](https://boosty.to/blackxdog/donate).

[![Поддержать на Boosty](https://img.shields.io/badge/Boosty-поддержать-f15f2c?logo=boosty&logoColor=white)](https://boosty.to/blackxdog/donate)

Поддержка помогает тестировать новые версии RouterOS и клиентов, сохранять
совместимость протокола и развивать security- и management-возможности проекта.

## 📄 Файлы проекта

| Путь | Назначение |
|---|---|
| [`deploy/routeros-install.rsc`](deploy/routeros-install.rsc) | Автоматическая установка RouterOS |
| [`deploy/routeros-update.rsc`](deploy/routeros-update.rsc) | Обновление контейнера RouterOS |
| [`deploy/compose.all-in-one.yaml`](deploy/compose.all-in-one.yaml) | Компактный all-in-one deployment |
| [`deploy/compose.yaml`](deploy/compose.yaml) | Раздельный deployment |
| [`docker/`](docker/) | Production multi-stage Dockerfiles |
| [`docs/`](docs/) | Архитектура, безопасность, совместимость и эксплуатация |
