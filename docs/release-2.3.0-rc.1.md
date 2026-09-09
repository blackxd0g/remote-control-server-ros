# 2.3.0-rc.1 — предварительный выпуск

Разрешён пользователем 2026-09-09 (REL-PUB-01). Архитектура: linux/amd64. ARM64 отложена; стабильный latest 2.2.1 и production не обновляются.

## Изменения

- Dashboard: список онлайн-пользователей с поиском и последней активностью.
- Официальный RustDesk 1.4.9: исправлены TOTP wire format, пагинация адресной книги и padded Base64 публичного ключа HBBS без ротации приватной пары.
- Отдельный relay: WSS с индивидуальными credentials, enrollment, acknowledgements и reconnect; постоянная очередь событий, карантин, диагностика, экспорт и архивирование с резервной копией.
- CI: Linux toolchain/target вместо Windows-настроек, musl-tools, Web build перед Go embed, интеграционные тесты настоящего HBBR. Проверки ARM64 временно отложены.

## Ограничения и обновление

Это кандидат для испытаний, не завершённый аналог RustDesk Pro. [Матрица официального клиента](official-client-acceptance.md) остаётся частичной: активная связь продолжала передавать ввод после force-relogin, хотя прежний токен и новые соединения отклонялись; подтверждённый relay-маршрут этого повторного теста требует проверки. Передача файлов требует полноценной Linux console/session. Shared profiles, другие ОС, внешний HTTPS/WSS, длительная нагрузка и multi-API не приняты.

Образы сервера и нового relay обновляются совместно. WSS-relay требует TLS, отдельный credential и постоянный outbox; общий internal.secret в новом режиме не используется. См. [контракт](relay-durable-delivery.md) и [эксплуатацию](relay-quarantine-maintenance.md). All-in-one по умолчанию сохраняет локальный legacy relay; подключение внешнего WSS-relay требует настройки gateway и TLS.

Перед обновлением остановить запись и сохранить отдельную копию тома /data и relay outbox. Новая схема outbox несовместима с прежней: откат выполнять на прежнем образе с его отдельной сохранённой копией данных, не запускать 2.2.1 поверх мигрировавшего outbox. Production в этой задаче не обновляется.

Публикация, digest и фактические проверки добавляются после выполнения, в журнал REL-PUB-01. Исторические проверки PAR-01 не являются новым прогоном этого выпуска.

## Фактическая публикация 2026-09-09

- [GitHub server prerelease](https://github.com/blackxd0g/remote-control-server-ros/releases/tag/v2.3.0-rc.1), [отдельный relay prerelease](https://github.com/blackxd0g/remote-control-relay/releases/tag/v2.3.0-rc.1). Архивы amd64 и SHA256SUMS приложены.
- Server Docker Hub: blackxdog/remote-control-server-ros:2.3.0-rc.1, digest sha256:b2e0a64ac402f4d7ef9ca9654f7738a8fa882f306eed75eb9c9d1f487df022fe.
- Relay Docker Hub: blackxdog/remote-control-relay:2.3.0-rc.1, digest sha256:26449c49042183a3f06991069e5d81d7792e36dae2aeb0dc7c3a3f9f472af442.
- [Server CI](https://github.com/blackxd0g/remote-control-server-ros/actions/runs/34293286243): все 5 jobs PASS. [Relay CI](https://github.com/blackxd0g/remote-control-relay/actions/runs/34293363360): PASS. Локальные проверки Web/Go/Rust, 38 Rust tests, 3 настоящих HBBR integration tests и container upgrade/rollback PASS.
- Обе публикации prerelease; stable/latest и production сохранены. Видимость отдельного relay — PRIVATE.
