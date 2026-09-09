# Проверки разработки

Команды сверены с исходниками и конфигурацией 2026-09-07. Это инструкция, а не отчёт об их успешном выполнении сегодня. Фактические запуски записывать в [журнал](journal/README.md), включая платформу, результат и пропуски. Подготовка окружения: [CONTRIBUTING.md](../CONTRIBUTING.md).

## Выбор объёма

| Изменение | Проверка |
|---|---|
| Markdown | Локальные ссылки, существование указанных файлов/команд, сохранность ID целей, согласованность статусов |
| Vue | typecheck и production build; для поведения формы — соответствующий UI-сценарий |
| Go API/хранилище | Форматирование, vet, целевой regression test; полный go test перед завершением существенного изменения |
| Rust HBBS/HBBR | fmt, clippy, workspace tests; протокольные проверки затронутого поведения |
| REL-02 | Go gateway/store tests и тест настоящего HBBR; отрицательные сценарии, перечисленные в контракте |
| Будущий релиз | Web + Go + Rust, сборки и запуск контейнеров amd64/arm64, проверка версии/архитектуры/данных и upgrade/rollback. Публикация отдельно разрешается пользователем |

## Web

PowerShell, из `art-web`, после установки зависимостей с frozen lockfile:

```powershell
pnpm run typecheck
pnpm run build
```

Скрипт build уже включает typecheck. В package.json нет отдельного автоматического browser-test скрипта. Успешная сборка не проверяет регистрацию в браузере или официальный RustDesk-клиент.

## Go

PowerShell, из `art-api`; предварительно собрать web для go:embed:

```powershell
gofmt -l .
go vet ./...
go test -count=1 ./...
```

`gofmt -l` должен вывести пустой список; его нулевой exit code сам по себе не означает правильное форматирование. Каждую команду запускать после успеха предыдущей. `-count=1` исключает выдачу кешированного результата за новый запуск.

Статическая Linux amd64 сборка из той же папки, в отдельном терминале (переменные сохраняются до его закрытия):

```powershell
New-Item -ItemType Directory -Force -Path bin | Out-Null
$env:CGO_ENABLED = '0'
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
go build -trimpath -o bin/art-api-linux-amd64 ./cmd/art-api
```

Не запускать go test с оставленным GOOS=linux на Windows: полученный тестовый бинарник не является Windows-программой. Сборка не заменяет запуск на целевой ОС.

## Rust

Из корня серверного проекта:

```powershell
cargo fmt --all -- --check
cargo clippy --locked --workspace --all-targets -- -D warnings
cargo test --locked --workspace
cargo build --locked --workspace --release
```

Последняя команда собирает для текущего host. Linux musl-поставка требует Linux-окружения с target/linker либо Dockerfile. Установка Rust target сама по себе не устанавливает системный linker. Docker all-in-one содержит отдельные стадии Rust-проверок и release-сборки.

## REL-02: gateway и настоящий HBBR

Из `art-api`, в окружении, где Go может запускать собственные тесты:

```powershell
go test -count=1 ./internal/httpapi -run '^TestRelaySecureControlEnrollmentPermitAndRevoke$' -v
go test -count=1 ./internal/store/sqlstore -run '^TestRelayEnrollmentSingleUseExpiryAndRevocation$' -v
```

Первый тест проверяет TLS gateway/enrollment/permit/revoke, второй — конкурентный одноразовый enrollment, expiry и revoke. Исходники: [gateway test](../art-api/internal/httpapi/relay_gateway_test.go), [store test](../art-api/internal/store/sqlstore/relay_credentials_test.go).

Тест настоящего бинарника — [TestRelayBinaryWSS](../art-api/internal/httpapi/relay_binary_test.go). Без `RDS_TEST_HBBR_BINARY` он **SKIP**, даже если весь go test завершился успешно.

В Linux/WSL из корня серверного проекта, после подготовки web, Go и Rust:

```sh
cargo build --locked -p art-hbbr
```

После успешной сборки, из `art-api` в том же Linux-окружении:

```sh
RDS_TEST_HBBR_BINARY="$(realpath ../target/debug/art-hbbr)" go test -count=1 ./internal/httpapi -run '^TestRelayBinaryWSS$' -v
```

Если настроен CARGO_TARGET_DIR, указать фактический абсолютный путь вместо стандартного. Linux-бинарник не запускать из Windows go test. Тест создаёт временные данные, локальные TCP/WS/TLS listeners и процесс HBBR; ему нужны права локальной сети и запуска процессов. Проверяет передачу TCP/WS/mixed, TLS trust, permit, terminate/ack, revoke и сохранение действующей пары. Требовать PASS именно этого теста, а не только пакета.

Также запускать `TestRelayBinaryDurableRestart` с тем же RDS_TEST_HBBR_BINARY: тест перезапускает настоящий HBBR, заменяет API/gateway и проверяет доставку snapshot и новую передачу данных. Store-тест `TestDurableRelayDeliveryRestartIsolationAndRotation` отдельно проверяет reopen БД и конкурентную дедупликацию. Подробности: [постоянная доставка](relay-durable-delivery.md).

Это не приёмка официального клиента, внешнего HTTPS/WSS стенда или ARM64. Также запускать TestRelayBinaryRotationQuarantine с RDS_TEST_HBBR_BINARY и TestRelayCompactionBatchBound. [Объём REL-OPS-01](relay-operations.md). Нагрузка, PostgreSQL runtime, полное обслуживание карантина и multi-API остаются открытыми; актуальный объём — [REL-02](relay-control-development.md) и [карта](roadmap-status.md).

## CI и доказательства

Текущий [ci.yaml](../.github/workflows/ci.yaml) запускает Go/Rust и контейнерные сборки с push=false. Web build выполняется внутри API/all-in-one Dockerfile. Тест настоящего HBBR не включён автоматически: CI не задаёт RDS_TEST_HBBR_BINARY. Успешный CI не закрывает всю REL-02/Pro приёмку.

В журнале различать: новый запуск, кешированный этап сборки, исторический результат и SKIP. Для Docker записывать архитектуру, локальный тег и реальный smoke-результат. Не публиковать секреты, дампы, enrollment-коды или приватные адреса. Ошибку окружения фиксировать отдельно от дефекта приложения, не объявлять тест пройденным.


## REL-OPS-02

`TestRelayBinaryRotationQuarantine` теперь также проверяет полный экспорт, подтверждение, архивирование, повтор и audit на настоящем HBBR. `TestRelayBinaryLoadOutage` требует одновременно `RDS_TEST_HBBR_BINARY` и `RDS_TEST_RELAY_LOAD=1`; занимает около минуты из-за реального 60-секундного отключения control. `TestRelayDeliveryConcurrentLoad` запускается с `RDS_TEST_RELAY_LOAD=1`.

`TestPostgresDurableRelayDelivery` требует `RDS_TEST_POSTGRES_DSN`, указывающий на **изолированную пустую тестовую БД**. Тест создаёт схему и фиксированные fixtures; повторять на новой БД. Никогда не передавать production DSN. Конкурентный load-тест использует уникальные синтетические записи и при наличии DSN проверяет также PostgreSQL. В текущем локальном прогоне применён PostgreSQL 17 в одноразовом контейнере без опубликованных портов, в internal Docker-сети. Без opt-in соответствующие тесты SKIP; это не PASS.

Контракт резервирования, границы замеров и результаты: [REL-OPS-02](relay-quarantine-maintenance.md).

## PAR-01: официальный клиент

Версия, стенд и фактические результаты — [матрица приёмки](official-client-acceptance.md). Целевые API-регрессии: `TestOfficialClientTFAChallengeAndOneTimeRecoveryCode`, `TestAddressBookOwnershipGrantsAndClientCompatibility`, `TestRegistrationRequiresAdministratorApproval`, `TestOfficialClientLoginLogoutAndDisableFlow`. Они проверяют wire protocol и ограничения backend; их PASS не заменяет успешный вход/соединение официального GUI.

После изменения auth/книг запускать `go test -count=1 ./...` и `go vet ./...`; для проверки текущего relay-контракта задавать `RDS_TEST_HBBR_BINARY` по правилам выше. Для GUI использовать отдельный профиль и синтетические учётные записи. Не ослаблять TOTP, lockout или TLS ради прохождения теста. ARM64 пока исключена по указанию пользователя.

### Уточнение CI 2026-09-09 (REL-PUB-01)

Предыдущее описание CI выше историческое. Обновлённый workflow явно выбирает Linux Rust 1.98.0 и Linux target поверх локальных Windows-настроек, устанавливает musl-tools, выполняет Web build перед Go и задаёт RDS_TEST_HBBR_BINARY для настоящего relay. Контейнерная проверка ограничена amd64 по DEC-006; поддержка ARM64 в Dockerfile сохранена.
