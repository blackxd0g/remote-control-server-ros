# Сверка с RustDesk Server Pro — 2026-09-07

База анализа: исходники Remote Control Server 2.2.1, имеющиеся автоматические тесты и release-документы, официальная документация Pro на дату проверки. **Полная функциональная эквивалентность Pro пока не достигнута.** Реализована существенная серверная основа; часть исторических отметок complete относилась к ограниченному этапу, а не к готовому пользовательскому сценарию.

Наличие маршрута, модели, очереди или protocol fixture не доказывает работу функции в официальном клиенте. В этом аудите не выполнялся полный прогон Windows/macOS/Linux/Android/iOS, внешний IdP/AD, нагрузочный тест или отказоустойчивый PostgreSQL-кластер. Проценты готовности без такого набора приёмки были бы произвольными.

## Матрица возможностей

| Область | Фактически есть | Остаток / статус |
|---|---|---|
| Аккаунты и сессии | Argon2id, JWT, серверные сессии, отзыв, блокировки, регистрация/одобрение; исправление регистрации поставлено в 2.2.1 | Реализован базовый сценарий. Нужна полная матрица официальных клиентов и восстановление учётной записи |
| 2FA, OIDC, LDAP/AD | TOTP/recovery codes, OIDC linking, LDAP TLS/group mapping | Реализация есть; внешние провайдеры, удаление/блокировка в AD, обновление групп и клиентский SSO требуют сквозной приёмки |
| Инвентарь и группы | Heartbeat, владение, группы/теги, архив, массовые операции, CSV | Реализована основа. Проверить объёмные списки и полную семантику видимости пользователей/устройств |
| Адресная книга | Personal/shared, grants, folders, favourites, клиентские API | Частично подтверждено: нужна синхронизация нескольких настоящих клиентов, отзыв доступа, конфликты и offline/reconnect; параметры pagination местами принимаются только ради wire-совместимости |
| ACL | Allow/deny, приоритеты, симулятор, HBBS отказывает до rendezvous/permit | Реализован контроль установления соединения. Совпадение всех правил групп/назначений с Pro не доказано |
| Control Roles | Отдельной модели/маршрутов ролей управления нет | Отсутствуют права конкретного оператора внутри сессии; это отдельный приоритетный модуль |
| Strategies | Назначения, 13 предопределённых ключей, heartbeat config_options, произвольные rustdesk.* | Частично; алгоритм отличается от Pro, произвольная передача ключа не подтверждает применение клиентом |
| Admin Roles | Настраиваемые наборы RBAC-разрешений и API tokens | Частично; нет полноценной модели global/individual/group области администрирования и соответствующей фильтрации всех объектов |
| SMTP и самостоятельное восстановление | Административное изменение пароля, уведомления в консоли, webhooks | SMTP, приглашения, email verification и forgot/reset password в исследованных исходниках отсутствуют |
| Аудит | Консольные события, conn/file ingestion, поиск/CSV, live connections, серверный relay lifecycle | Основа есть; нужны сроки хранения, видимость по scope, связь сессий/методов входа, заметки и email/security alarm сценарии |
| Завершение соединений | В 2.1.0 добавлены UUID-корреляция, terminate/ack и разрыв relay-пары | Реализовано для relay. Сервер не может гарантированно закрыть уже установившийся прямой P2P-канал |
| Несколько relay | Регистрация, health/load/latency, метрики, выбор узла | Есть; нет GeoIP выбора ближайшего к клиенту. Latency от API не равна latency клиента |
| Отдельный relay | Подготовлен минимальный образ из HBBR релиза 2.2.1, Compose и RouterOS-шаблон | Локальная поставка отдельного компонента; подключение к выбранному production-узлу — отдельный этап |
| Custom Client Generator | Профили/branding, подписанные config artifacts, очередь, worker credentials, leases, upload/download | Серверная часть есть. Реального native worker с выпуском установщиков в этом репозитории нет |
| Массовое развёртывание клиента | Выдача конфигурации и управление устройствами | Нужны воспроизводимые install/update/uninstall, подпись, rollback и практические сценарии MDM/GPO |
| Web client | Web console и WS-транспорт HBBS/HBBR | Браузерный remote desktop отсутствует: нужны клиентское приложение, доставка и end-to-end сценарии |
| Backup/operations | SQLite backup/restore, диагностика, Prometheus, support bundle, leases | Реализован набор инструментов. Нужны измеренные RPO/RTO, restore drill и план обслуживания растущей БД |
| HA | SQL node heartbeat/leader leases, PostgreSQL dialect, reconciliation auth cache | Только подготовка к HA. Нет доказанной межузловой доставки событий и полного failover API/HBBS/relay |

Официальные основания сравнения: [обзор Pro](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/), [консоль](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/console/), [адресная книга](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/address-book/), [ACL](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/permissions/), [Admin Role](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/admin-role/), [SMTP](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/smtp/).

## Важные расхождения в коде

### 1. Control Roles нельзя заменить ACL

Pro различает разрешение подключиться и разрешённые действия оператора после подключения. Control Role имеет три состояния разрешения: клиентское значение, включить, выключить; встроенные Default/Not Logged. Документация перечисляет 12 прав, включая ввод, буфер, файлы, аудио, камеру, терминал, туннель, перезапуск, запись, печать, блокировку ввода и изменение удалённых настроек. Для управляемой стороны указана версия от 1.4.5, Android пока исключён. [Источник](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/control-role/).

У нас [HBBS auth cache](../art-core/src/auth/cache.rs) принимает решение до соединения, а [heartbeat](../art-api/internal/httpapi/inventory.go) доставляет настройки устройства. Отдельной сущности ControlRole не найдено. Нужны модель, UI, назначения и совместимый обмен с клиентом; блокировка clipboard через настройку устройства не эквивалентна персональному праву оператора.

### 2. Стратегии отличаются и требуют общей спецификации Go/Rust

Pro выбирает одну эффективную стратегию с приоритетом device → user → device group. [Источник](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/strategy/).

Наш [Go Resolve](../art-api/internal/strategy/service.go) объединяет настройки нескольких стратегий: сначала числовой priority, затем specificity; при равном priority device_group выше user. Rust [AuthCache](../art-core/src/auth/cache.rs) имеет отдельный алгоритм. Go использует UpdatedAt для разрешения равенства, Rust сортирует только priority/specificity; одинаковые приоритеты требуют явного tie-break. Кроме того, Go сопоставляет user с владельцем устройства, Rust — с авторизованным контроллером. Это разные контексты: их нужно явно определить и проверить совместными fixtures, прежде чем объявлять политику одинаковой.

Нельзя молча изменить существующую семантику: нужен режим совместимости/миграция, preview итоговой политики, тесты удаления/отключения и доказательство применения настроек клиентом после reconnect.

### 3. Generator пока заканчивается очередью

[managedclient/builder.go](../art-api/internal/managedclient/builder.go) и [builder integration tests](../art-api/internal/managedclient/builder_integration_test.go) реализуют доверие к worker, lease и загрузку результата. ManifestBuilder формирует конфигурацию. Это полезная работа, но она не компилирует нативный клиент. Нужны отдельные сборочные workers с закреплёнными исходниками/toolchains, установщиками и проверкой подписи. macOS signing/notarization и Windows signing требуют соответствующих внешних учётных данных; Linux build не заменяет их.

### 4. HA пока нельзя считать готовым

[cluster/coordinator.go](../art-api/internal/cluster/coordinator.go) координирует фоновые задачи. Однако [events/hub.go](../art-api/internal/events/hub.go) хранит stream revision/history и подписчиков в памяти процесса, с ограниченным буфером. Это не межузловой журнал. Периодические snapshot в [auth/sync.rs](../art-core/src/sync.rs) помогают восстановить cache, но не доказывают своевременную глобальную инвалидацию или надёжное выполнение automations на другом API.

Нужны transactional outbox/общая доставка, глобальные revision semantics, Postgres integration tests, управление адресацией нескольких API/HBBS, failover/partition/restart tests. Разрыв сессии при потере обслуживающего relay допустим только как документированный reconnect, не как бесшовный failover.

### 5. Удалённый relay: граница доверия должна стать уже

[HBBR main](../art-hbbr/src/main.rs) требует permit на UUID с двумя использованиями; управляющий UDP проверяет общий internal secret. Этот же привилегированный секрет используется внутренним API. Для 2.2.1 отдельный relay допустим в доверенной сети/VPN, с ограничением UDP по источникам.

До размещения relay в менее доверенной сети нужны отдельные отзываемые credentials на узел, отдельный control_address, защита от replay и подтверждение выдачи permit. Сейчас адрес вычисляется из relay hostname и фиксированного UDP 21119 в [HBBS](../art-hbbs/src/server.rs) и [relaycontrol](../art-api/internal/relaycontrol/client.go). Поэтому документация установки требует split DNS/VPN.

[telemetry.rs](../art-hbbr/src/telemetry.rs) использует bounded in-memory очередь lifecycle без устойчивого retry/spool; [Cargo.toml](../Cargo.toml) — настройки reqwest workspace нужно учитывать при выборе канала: текущая Rust-сборка internal HTTP не имеет TLS transport. Нужны timeout для handshake, предел числа ожидающих клиентов/permits и тесты перегрузки. Это найденные области усиления, а не утверждение о проверенном exploit.

Автовыбор в [relay/monitor.go](../art-api/internal/relay/monitor.go) и [AuthCache](../art-core/src/auth/cache.rs) опирается на доступность/нагрузку; GeoIP клиента не реализован. Pro документирует GeoLite2 City и выбор ближайшего online relay. [Источник](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/relay/).

### 6. Проверка эксплуатации и поддержки

[client_audit.go](../art-api/internal/httpapi/client_audit.go) принимает клиентские отчёты, поэтому полнота аудита зависит от клиента и доставки. Неизменяемость через обычный API не делает таблицу tamper-proof для администратора БД. Нужны проверки доверия к device id/uuid, повторов событий и потери доставки. Pro отдельно описывает категории журналов, сроки хранения и scope просмотра. [Источник](https://rustdesk.com/docs/en/self-host/rustdesk-server-pro/audit-logs/).

В production backup 2.2.1 зафиксирован размер БД около 415 MB при 129 устройствах. Это повод измерить вклад таблиц/индексов и темп роста, а не доказательство утечки. Также крупные [httpapi/server.go](../art-api/internal/httpapi/server.go) и [sqlstore/store.go](../art-api/internal/store/sqlstore/store.go) затрудняют расширение: декомпозицию выполнять при изменении соответствующих модулей, без отдельного масштабного переписывания.

## Предлагаемый порядок до функциональной полноты

План и статусы ведутся в [roadmap-status.md](roadmap-status.md). Ниже приоритеты, не обещанные номера релизов или сроки.

| Этап | Приоритет | Проверяемый результат |
|---|---|---|
| PAR-01: контракт и стенд | P0 | Закреплённые версии официальных клиентов, учётные записи разных ролей, сценарии login/ACL/AB/relay/deny/revoke, отчёты по ОС |
| PAR-02: политики доступа | P0 | Control Roles, scoped Admin Roles; единая спецификация Strategies и миграция; разрешения подтверждены действиями клиента |
| PAR-03: самостоятельный аккаунт | P1 | SMTP TLS, очередь/повторы, приглашение, verification/reset одноразовыми токенами, запрет enumeration и повторного использования |
| PAR-04: native clients | P1 | Реальный worker → подписанный installer → установка → branding/config → соединение → update/rollback на каждой заявленной ОС |
| PAR-05: браузерный клиент | P1 | Собственный hosted web client, HTTPS/WSS, вход/адресная книга/удалённый экран/ввод/файлы, session logout/revoke |
| REL-02: удалённые relay | P0 перед менее доверенной сетью; GeoIP P1 | Scoped keys, private control endpoint, permit ack/replay protection, лимиты, GeoIP fallback, отключение/ротация узла |
| PAR-06: audit и каталог | P1 | Scoped visibility всех API/export, AB multi-client/offline тесты, retention/delivery, нагрузка на списки и аудит |
| PAR-07: enterprise resilience | P2 | OIDC/AD live matrix, Postgres/outbox/failover, restore drill с измеренными RPO/RTO, нагрузочные и upgrade/rollback тесты |
| PAR-08: финальная приёмка | Release gate | Каждый пункт матрицы имеет passed report либо явно согласованное исключение; полная Pro-совместимость до этого не заявляется |

Начать предлагаю с PAR-01 и PAR-02: они проверяют и устраняют расхождения в безопасности уже подключённых клиентов. Relay-пакет можно применять отдельно в доверенной сети, не ожидая native builder или web client.
