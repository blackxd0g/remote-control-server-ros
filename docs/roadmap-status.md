# Roadmap status

## Правила ведения дорожной карты

По требованию пользователя от 2026-09-07 все наши планы по серверному проекту обязательно фиксируются в этом документе до начала реализации.

- Для новых задач указывать ожидаемый результат, статус и критерии готовности.
- Предложения отделять от согласованных планов; приоритет и целевой релиз указывать, когда они определены.
- При начале, завершении, переносе или изменении объёма работ обновлять соответствующую запись.
- Завершение фиксировать только после необходимых проверок; историю реализованных этапов сохранять.

## Публикация на GitHub — в работе (2026-09-07)

- По запросу пользователя загрузить актуальные исходники, аудит Pro, дорожную карту и отдельный relay в существующий `blackxd0g/remote-control-server-ros`.
- Relay разместить отдельным каталогом `relay/`; бинарные tar-артефакты поставить через отдельный GitHub Release, без изменения Docker Hub latest.
- Проверить diff, исключение секретов/локальных данных и корректность ссылок. Завершение: commit доступен на GitHub, файлы релиза совпадают по SHA-256.

Historical complete labels below describe the stated implementation milestone, not full RustDesk Pro parity. Current functional gaps and acceptance gates are recorded in the 2026-09-07 audit and the PAR/REL plan below. Client-visible parity requires a successful real-client scenario in addition to models, APIs and automated coverage.

## Сверка с RustDesk Server Pro и отдельный relay — аудит и локальная поставка выполнены (2026-09-07)

- Цель: функциональная полнота аналога RustDesk Server Pro, подтверждённая реальными сценариями официального клиента, а не только наличием моделей и API.
- Провести аудит кода и тестов версии 2.2.1, сверить с текущей официальной документацией Pro; составить матрицу «реализовано / частично / отсутствует / не проверено» с доказательствами и приоритетами.
- Пересмотреть старые отметки complete там, где они описывают лишь серверную основу; записать оставшиеся этапы и критерии приёмки.
- Подготовить отдельный минимальный relay-контейнер: один HBBR, без API, Web, HBBS и БД, amd64/arm64, совместимость с авторизацией основного сервера, документация Compose/RouterOS, проверка передачи данных и отказа без разрешения.
- Публикацию и установку нового relay на конкретном узле учитывать отдельно от локальной сборки и тестирования.
- Итог: [аудит Pro parity](pro-parity-audit-2026-09-07.md) составлен по исходникам и официальной документации. Полнота Pro пока не достигнута; исторические complete уточнены для Strategies, HA, Stable и исправлена устаревшая граница termination.
- REL-01 выполнен: [отдельный проект relay](../relay/README.md), scratch-образ с единственным HBBR 2.2.1, amd64/arm64, non-root/read-only, Compose, RouterOS-шаблон и tar-артефакты. Проверены реальные TCP/WS/mixed данные, отказ без permit/с неверным токеном/по expiry, лимит двух peers, terminate/ack и закрытие обоих концов, регистрация телеметрии в изолированном API. Это протокольный smoke, не полная приёмка официального клиента. ARM64 проверен под эмуляцией.
- Публикация отдельного relay в registry и установка на новый production-узел не выполнены: целевой узел ещё не определён. Существующий production в рамках этой задачи не изменялся.

## Остаток до функциональной полноты Pro — предложенный порядок

Цель согласована пользователем; ниже предложены этапы по результатам аудита. Номера релизов, сроки и полная матрица целевых ОС пока не утверждены. Реализация этих этапов не включена в выполненный аудит.

| ID | Приоритет / статус | Результат и критерии готовности |
|---|---|---|
| PAR-01 | P0, предложен первым | Версии официальных клиентов и стенд; сквозные login/approval/2FA/ACL/AB/relay/revoke сценарии с отчётами по ОС, включая отрицательные проверки |
| PAR-02 | P0, предложен | Control Roles и scoped Admin Roles; спецификация Strategies, общие fixtures Go/Rust, безопасная миграция текущих приоритетов; права внутри сессии реально соблюдаются клиентом |
| REL-02 | P0 до менее доверенной сети, предложен | Отдельные отзываемые relay credentials, private control_address, permit ack/replay protection, handshake timeout и лимиты; rotation/restart/reconnect проверены |
| PAR-03 | P1, предложен | SMTP TLS и очередь доставки, приглашение/email verification/reset password; одноразовые истекающие токены и отрицательные проверки |
| PAR-04 | P1, предложен | Изолированные native workers, подписанные установщики и metadata; build/install/connect/update/rollback на каждой заявленной платформе |
| PAR-05 | P1, предложен | Полноценный hosted web client через HTTPS/WSS: вход, адресная книга, экран/ввод, файлы, отзыв доступа |
| REL-03 | P1, предложен | Географический выбор relay относительно клиента, fallback/health/load и управляемое выключение узла; проверка с разных регионов |
| PAR-06 | P1, предложен | Scoped audit/API/export, AB sync/offline/conflicts, pagination, retention и контроль доставки; измеренный рост БД и нагрузочный отчёт |
| PAR-07 | P2, предложен | Live OIDC/AD проверки, Postgres integration, межузловой outbox/events, API/HBBS failover и partition, restore drill с RPO/RTO; декомпозиция затрагиваемых крупных модулей |
| PAR-08 | Финальная приёмка, ожидает предыдущие этапы | Каждый пункт матрицы Pro имеет воспроизводимый passed-сценарий или явно согласованное исключение; upgrade/rollback проверены |

REL-DEPLOY: отдельная установка relay ожидает выбора узла и адресации. Перед установкой подготовить конкретный конфиг/VPN/firewall/secret mount, затем проверить выдачу permit от настоящего HBBS, выбор relay официальным клиентом и завершение сеанса из консоли. RouterOS-шаблон не считается production-проверкой.

## 0.1 Authentication Core — complete

- Argon2id local authentication, first-run bootstrap, rate limiting, server-side sessions, strict JWT claims, logout/revoke/disable/force-relogin, and immutable audit events.
- Authentication is enforced by HBBS before target lookup, punch-hole instructions, or relay permits. The last-valid authorization cache is event-driven, reconciled, persisted, and fail-closed on an empty installation.
- Official RustDesk login, account, heartbeat, inventory, audit, TCP, and WebSocket flows are supported.

## 0.2 Access Control — complete

- User groups, device groups, tags, personal/shared address books, folders, favourites, search, grants, and current-client address-book routes.
- Server-side ACL permissions are resolved and enforced by HBBS before a connection is established.

## 0.3 Strategies — complete

Аудит 2026-09-07: завершена собственная серверная модель; Pro parity частична. Алгоритм объединения/приоритетов отличается от Pro, требуется PAR-02 и сквозная проверка клиента.

- Global, user, user-group, device, and device-group assignments with deterministic priority and specificity inheritance.
- Security-sensitive settings are enforced by HBBS; compatible client settings are delivered through the official heartbeat response.

## 0.4 Enterprise Auth — complete

- TOTP with one-time recovery codes and configurable enforcement modes.
- Generic OIDC account linking and login.
- LDAP/Active Directory over LDAPS or StartTLS with explicit group mappings and controlled auto-provisioning.
- Persistent custom RBAC roles and scoped, revocable deployment API tokens.

## 0.5 Infrastructure — complete

- Multiple relay registration, health/latency/load history, regional selection, live HBBR telemetry, and authenticated HBBS control.
- API, database, HBBS, HBBR, device, session, CPU, RAM, connection, and traffic visibility.
- Persistent administrator notifications and signed webhook delivery with retry history and SSRF protection.

## 0.6 Client Management — server side complete

- Managed client profiles, scoped assignments, versioned effective configuration, branding metadata, and official-heartbeat delivery.
- Immutable signed configuration artifacts and a persistent native-build queue with atomic capability-aware claims, expiring leases, heartbeat renewal, cancellation, retry and audited completion.
- Dedicated builder authentication, persistent worker inventory, SHA-256 verified binary uploads and artifact downloads are complete. Native compilation itself deliberately remains in dedicated sandboxed platform workers, outside the API/HBBS/HBBR container and repository.

## 0.7 Production Operations — complete

- Token-protected Prometheus gauges for users, sessions, devices, HBBS/HBBR, relays, traffic, CPU, RAM, uptime and auth-cache revision.
- Authenticated server self-diagnostics for database responsiveness, persistent storage, secret permissions, service heartbeats and reverse-proxy trust configuration.
- Explicit trusted-proxy CIDRs with right-to-left `X-Forwarded-For` resolution; forwarding headers from untrusted peers are discarded before authentication, rate limiting or audit.
- Online SQLite snapshots plus bounded, read-only backup upload inspection using SQLite `quick_check`, required-schema validation and audited results. Inspection never replaces the active database.
- Hardened browser response headers, durable metrics credentials and production reverse-proxy documentation.

## 0.8 Device Lifecycle & Fleet Operations — complete

- Active and archived inventories are separated without losing heartbeat history or management metadata.
- Devices can be archived, restored, and permanently removed only after archival; a later heartbeat can safely rediscover a removed device.
- Transactional bulk assignment supports device groups plus independent tag additions and removals.
- UTF-8 CSV export is spreadsheet-safe. Bounded CSV import validates schema, duplicate IDs, field limits, device groups, active inventory membership, and applies the entire file atomically.
- Lifecycle, bulk-update, and import operations are permission-protected and recorded in the immutable audit trail.

## 0.9 Audit Explorer & Connection Analytics — complete

- Indexed server-side filtering by event type, result, actor, target device, IP address, free-text term, and UTC date range.
- Exact result totals and bounded pagination keep the console responsive with large audit histories.
- Filter-aware counters cover total events, allowed and denied connections, and failed authentication attempts.
- Detailed event inspection exposes session, controller, target, reason, and structured metadata without flattening the immutable record.
- Filter-aware UTF-8 CSV export streams the audit history in bounded database pages and protects spreadsheet cells from formula injection.

## 1.0 Session Security Center — complete

- Unified active, revoked, and expired session inventory with the associated username, display name, user state, device identity, client, IP, creation, activity, and expiry data.
- Indexed server-side lifecycle filtering, user/device/IP/client search, exact totals, and bounded pagination.
- Administrators can select and revoke up to 500 active sessions in one operation; every revocation is propagated immediately to HBBS through the existing event-driven authentication cache.
- The current administration session is identified server-side and protected from accidental bulk revocation.
- Bulk revocation requires `sessions.revoke`, validates the complete selection before mutation, and creates one immutable administrative audit event containing the affected session IDs.

## 1.1 Backup & Disaster Recovery — complete

- Scheduled and on-demand online SQLite snapshots are stored under the persistent `/data/backups` directory with configurable retention.
- Every generated or uploaded database is validated with SQLite `quick_check` and required-schema inspection before it is accepted.
- Administrators can list, download, and delete snapshots or stage an uploaded snapshot for recovery through a dedicated RBAC-protected console.
- Restore is applied before services start, verifies a persisted SHA-256 marker, preserves the previous database plus WAL/SHM files, and atomically replaces the active database.
- Backup creation, deletion, restore staging, cancellation, and rejected restore attempts are recorded in the immutable audit trail.

## 1.2 Live Connection Operations — complete

- Official-client `connection_started`, `connection_updated`, and `connection_closed` telemetry is correlated into active, stale, and recently closed connections.
- Connection audit now resolves the authenticated operator and server session from the controller device, so the console can show who connected to each RustDesk ID.
- The administration console provides an auto-refreshing connection center with controller device, target ID, connection type, IP address, start time, and duration.
- Administrators can contain an attributed live connection in one action: the operator session is revoked through the normal event-driven auth path, reconnects are blocked immediately, the live projection is closed, and the action is written to the immutable audit trail.
- Historical 1.2 boundary: transport interruption was not guaranteed. Superseded by 2.1.0: HBBR UUID correlation, terminate/ack and cancellation now close established relay pairs. Direct P2P streams still cannot be reliably torn down after rendezvous by server design.

## 1.3 Security & Compliance — complete

- Password requirements are centrally configurable and enforced for registration, user creation, password changes, and administrator edits without weakening existing Argon2id storage.
- Username-based brute-force state and lockouts are persisted in the shared database, survive restarts, normalize login names, expose `Retry-After`, and are cleared after a successful authentication.
- TOTP enrollment, mandatory modes, one-time recovery codes, administrator reset, server-side session revocation, security audit events, and hardened proxy/header handling remain enforced.

## 1.4 Advanced Access Control — complete

- ACL rules support explicit `allow` and `deny` effects. Lower priority numbers take precedence; at the same priority an explicit deny wins.
- API simulation and HBBS pre-connection enforcement use the same deterministic rule semantics. Existing installations migrate old rules to `allow` without manual intervention.
- The console includes an effective-access simulator with per-rule matching trace, winning effect, and priority, so administrators can verify a policy before relying on it.

## 1.5 Event-driven Automation — complete

- Persistent automation rules subscribe to the existing internal event stream and can filter domain or immutable audit event fields without polling.
- Rules provide durable execution history, per-rule throttling, severity, RBAC-protected management APIs, administrator notifications, and a dedicated console.
- Outbound actions reuse the signed webhook delivery pipeline with retry and SSRF protection; no shell-command action is exposed and generated events cannot recursively invoke automation.

## 1.6 High Availability & Cluster Readiness — complete

Аудит 2026-09-07: complete относится к node inventory и leader leases. Полноценная HA не принята: in-memory event hub не обеспечивает межузловую доставку, failover требует PAR-07.

- API instances have a persistent node identity, database-backed heartbeat inventory, and an administrator-visible cluster state endpoint.
- Atomic renewable leases work on SQLite and PostgreSQL semantics and prevent an active lease from being stolen before expiry.
- Relay monitoring, webhook delivery, and scheduled backups run under separate leader leases; event ingestion remains local to every API node so events are not dropped on followers.
- The dashboard exposes active API nodes and leases, and existing saved layouts automatically acquire newly introduced widgets.

## 1.7 External Builder Trust Boundary — complete

- The shared bootstrap credential can only register a worker. Registration returns a unique high-entropy worker credential once, and only its SHA-256 digest is persisted.
- Heartbeat, claim, lease renewal, payload, completion, and failure routes require the individual worker credential and bind every operation to the authenticated worker identity.
- Re-registering a worker explicitly rotates its credential. A worker cannot impersonate another worker ID or use the bootstrap credential to claim work.
- Native compilation remains intentionally outside this image. The server-side queue and protocol are stable; the separately isolated worker container can be connected later without access to the database or server secrets.

## 1.8 Supportability — complete

- Administrators can download an audited support ZIP from a dedicated console page.
- The bundle contains version, safe runtime policy, cluster state, inventory counters, and a bounded redacted audit timeline.
- Passwords, hashes, JWTs, worker credentials, usernames, IP addresses, file names, and connection content are excluded by construction.

## 1.9 Upgrade Readiness — complete

- Database changes through 2.0 are additive and migrate automatically for existing SQLite and PostgreSQL installations.
- Persistent `/data` keeps the legacy database filename, server identity, JWT/session material, branding, backups, and runtime configuration to avoid a destructive rename during upgrade.
- A documented pre-upgrade backup, rollback boundary, release checklist, and immutable versioned image tag are required for the 2.0 release gate.

## 2.0 Stable — complete

Аудит 2026-09-07: исторический stable-релиз серверной основы, не заявление о полнофункциональном RustDesk Pro. Native generator и web client остаются незавершёнными пользовательскими сценариями.

- Authentication, pre-connection enforcement, ACL, Strategies, enterprise authentication, audit, automation, cluster coordination, backup/restore, fleet operations, managed-client control plane, and supportability have a persistent production implementation.
- The external native Builder worker remains an optional separate component and is not bundled into the lightweight RouterOS image.

## Исправление регистрации новых пользователей — выполнено в 2.2.1 (2026-09-07)

- План: проверить публичную форму, API регистрации, сохранение пользователя и процесс одобрения; воспроизвести и исправить сбой.
- Ожидаемый результат: при включённой регистрации корректные данные создают пользователя; при обязательном одобрении доступ разрешается только после решения администратора, а ошибки понятно отображаются в форме.
- Критерии готовности: регрессионные проверки найденного дефекта, проверка запрета отключённой регистрации и обхода одобрения, необходимые проверки Go и сборка Vue/TypeScript.
- Публикация и установка исправления: статус уточняется после проверки реализации.
- Диагностика: сервер возвращает `enabled=true`, `approval_required=true`; локальные `TestRegistration*` проходят. Конкретный пользовательский сбой пока не воспроизведён, запрошено описание ошибки. В форме обнаружены отдельные недостатки: игнорирование `enabled` и отсутствие подсказок по требованиям к данным.
- Сбой воспроизведён через браузер на версии 2.1.1: пароль `Test12345` вызывает непрозрачную ошибку `account could not be registered`; подходящий пароль с тем же логином создаёт заявку. План исправления: публично отдавать действующую политику пароля, показывать её до отправки формы, локализовать ошибку и учитывать доступность регистрации. В локальном API уже есть обработка ошибки политики с HTTP 400; требуется включить её в проверяемое исправление.
- Реализовано локально: публичная политика берётся из auth-service; форма показывает требования, русское сообщение об отказе, состояния загрузки и отключения регистрации. Проверены `TestRegistration*`, `TestUserCreationPasswordPolicyErrors` (включая изменение действующей политики), `go vet ./...`, Vue/TypeScript и Vite build.
- На сервере созданы только тестовые заявки `registration-test-20260907090501` и `registration-ui-test-0907`, обе `pending`, без одобрения. Полный Go-прогон и локальный UI smoke пока не завершены: процессы зависали без вывода. Установка исправления на MikroTik не выполнена; задача остаётся в работе до завершения проверок и поставки.
- Статическая сборка Go API для `linux/amd64` с `CGO_ENABLED=0` также прошла.
- Итог: проверки завершены в Docker, форма проверена в браузере (ошибка короткого пароля и успешная заявка). Исправление опубликовано в 2.2.1 и установлено на MikroTik; прежние ограничения локальных проверок сняты.

## Release gate

### Фикс-релиз 2.2.1 и обновление MikroTik — выполнено (2026-09-07)

- Включить исправление регистрации и парольную политику по умолчанию от 8 символов без требований к составу.
- Выполнить проверки Vue, Go и Rust, собрать и проверить all-in-one образ; опубликовать версионный тег и обновить `latest` с сохранением поддерживаемых архитектур.
- Сделать repull контейнера `rustdesk_server_routeros` на MikroTik 10.0.47.1; подтвердить версию, здоровье компонентов, сохранность данных и политику регистрации.
- При сборке обнаружено принудительное `TARGETARCH=amd64` в Go-стадии; убрать переопределение автоматической архитектуры BuildKit в all-in-one и API Dockerfile. Проверить ELF-архитектуру всех трёх бинарников обоих образов перед публикацией.
- Подготовлены VERSION 2.2.1 и release notes; Vue/TypeScript/Vite, `go vet ./...` и целевые интеграционные тесты регистрации прошли.
- Блокер: Docker Desktop не запускается из-за недоступного `AppData/Local/Docker/run/sailor-ingest.sock`. Переименование и удаление отдельного сокета не удалось. Автоматическая проверка отклонила переименование всей временной папки `run` из-за риска для активных служб/сокетов; требуется явное разрешение пользователя на восстановление Docker. Публикация и repull не выполнены.
- Блокер снят: пользователь запустил Docker Desktop. Linux Go tests/vet и Vue production build прошли; неизменённая Rust-стадия fmt/clippy/tests подтверждена BuildKit-кешем. amd64 контейнер прошёл запуск, проверку трёх ELF-бинарников, версии и регистрации с границей 7/8 символов.
- Перед обновлением создана серверная копия `rustdesk-backup-20260907-062625.551201917.db`, SQLite `quick_check=ok`; исходное состояние: 6 пользователей, 129 устройств, API/HBBS/HBBR/DB online.
- Итог: multi-architecture сборка завершена; ARM64 и amd64 прошли проверку ELF всех трёх бинарников, health, версии, отклонения 7-символьного пароля и регистрации 8-символьного со статусом pending. Vue TypeScript/Vite, полный Go tests/vet и статические сборки прошли; Rust release собран для обеих архитектур.
- Опубликованы `blackxdog/remote-control-server-ros:2.2.1` и `latest`, общий OCI digest `sha256:e1ad1e4a46601d935082114d21e09892915376c20f6449dba9605a67eef447b8` (linux/amd64 + linux/arm64).
- Repull `rustdesk_server_routeros` выполнен. На MikroTik подтверждены UI 2.2.1, image-id `8b9f479644db1651c9bcd4dd81e3f8888d0dab56496e82c0b6774b974a7af28c`, API/HBBS/HBBR/DB online, 6 пользователей и 129 устройств (82 online на момент проверки). Минимум 8 символов, require_login и обязательное одобрение сохранены.

### Упрощение парольной политики — выполнено (2026-09-07)

- Согласовано: минимум 8 символов, без обязательных заглавных/строчных букв, цифр и спецсимволов.
- План: обновить значения по умолчанию в проекте и сохранить политику через API работающего сервера.
- Проверка: 7 символов отклоняются, 8 символов принимаются; прочие настройки сохраняются.
- Применено через `PATCH /api/admin/settings` и подтверждено повторным чтением: минимум 8, все четыре требования к составу выключены, остальные настройки не изменены. Перезапуск не нужен, политика сохранена в БД.
- Проверка на работающем сервере: 7 строчных букв → отказ; 8 строчных букв → HTTP 202, `pending` (заявка `policy-test-20260907091857`).
- Обновлены значения по умолчанию в API и форме настроек. Тесты `TestPasswordPolicy*` прошли, включая длину Unicode; команда Go сообщила об ошибке очистки временного exe, занятого другим процессом, после успешного выполнения тестов.

Every release must pass Vue TypeScript validation and production build, Go formatting/vet/tests/static linux-amd64 build, and Rust formatting/clippy/tests/release build. Container publication and target-host deployment are separate post-gate operations.

## Runtime security configuration

The administrator console persists operational security policy in the database. Environment values remain first-run defaults; once an administrator saves a value, it survives restarts and takes precedence. `require_login` and `require_device_deployment` are propagated to every HBBS node through the internal event stream and periodic snapshot reconciliation. Registration, access-token/session lifetimes, and the TOTP enforcement mode apply immediately to new authentication operations. Existing signed tokens keep their original expiry, while revocation and force-login remain available for immediate invalidation.
