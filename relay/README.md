# Remote Control Relay 2.2.1

Отдельный управляемый relay для **Remote Control Server 2.2.1**. В образе только статический `art-hbbr`: нет HBBS, API, Web UI, SQLite, shell, пакетного менеджера или supervisor. PID 1 — HBBR, UID/GID 65532, root filesystem может быть read-only. Поддерживаются linux/amd64 и linux/arm64.

Это отдельный процесс relay, а не автономный сервер авторизации. HBBS основного сервера проверяет сессию/ACL и выдаёт разрешение на пару соединений. Без разрешения relay закрывает соединение. Relay отправляет телеметрию и события основному API; он не хранит пользователей или пароли.

## Сборка

```sh
docker buildx build --platform linux/amd64,linux/arm64 --provenance=false --load -t remote-control-relay:2.2.1 .
```

Для Docker без containerd image store собирайте одну архитектуру через `docker build -t remote-control-relay:2.2.1 .`. Dockerfile извлекает только HBBR из опубликованного проверенного server-релиза, зафиксированного digest. Это обеспечивает совпадение протокола и не включает остальные слои сервера в итоговый образ. Исходники бинарника находятся в [`art-hbbr`](../art-hbbr), а сборка из исходников — в [`docker/hbbr.Dockerfile`](../docker/hbbr.Dockerfile).

## Подключение к основному серверу

1. Создайте приватную/VPN-связность между relay, API и HBBS. В этой версии Rust internal HTTP-клиент собран без TLS, а управляющий канал UDP передаёт общий токен. Эти каналы должны идти внутри доверенной сети/VPN, а не через открытый Интернет.
2. Передайте **копию** `/data/secrets/internal.secret` основного сервера на relay по защищённому каналу. Это привилегированный внутренний секрет, не публичный RustDesk Key и не JWT secret. Отдельного ограниченного ключа relay в 2.2.1 ещё нет. Не генерируйте независимый секрет: HBBS не сможет выдать permit.
3. На Linux файл должен быть доступен UID 65532: например `chown 65532:65532 secrets/internal.secret` и `chmod 0400 secrets/internal.secret`. Не помещайте секрет в Git или образ.
4. Скопируйте `.env.example` в `.env`, укажите уникальный ID relay, публичное имя, приватный bind-IP и URL API. Запустите `docker compose up -d --build`.
5. HBBR публикует телеметрию в центральный API и появляется в «Relay Servers». Телеметрия отмечает узел healthy; API дополнительно проверяет TCP-доступность. HBBS может выбрать зарегистрированный узел. ID и hostname:port должны быть уникальны: при совпадении hostname:port API обновляет существующую запись, сохраняя её ID.
6. Сейчас управляющий адрес вычисляется из **того же hostname**, который указан в `RDS_HBBR_PUBLIC_ADDRESS`, с портом 21119/UDP. Для отдельного узла настройте split DNS: API/HBBS должны разрешать это имя в приватный/VPN-IP, клиенты — в публичный. Отдельное поле control_address запланировано в дорожной карте.

| Канал | Доступ |
|---|---|
| 21117/TCP | Клиенты: relay data |
| 21119/TCP | Клиенты: WebSocket relay; WSS при необходимости завершает внешний reverse proxy |
| 21119/UDP | Только HBBS/API по VPN: permit и terminate. Ограничить источники firewall |
| Исходящий HTTP к API:21114 | Только по приватной сети/VPN: telemetry и lifecycle |

Compose привязывает UDP к обязательному приватному IP, но фильтрация по источникам остаётся обязанностью firewall. Для NAT не меняйте управляющий порт 21119: настраиваемого порта в модели основного сервера пока нет.

## Минимальный пример RouterOS

Ниже шаблон для уже настроенных VETH, маршрутизации и VPN. Сначала импортируйте локальный tar-образ `outputs/remote-control-relay-2.2.1-amd64.tar` в Files. Для ARM64 используйте файл с суффиксом `-arm64.tar`. Этот проект пока не опубликован как отдельный образ в registry. Синтаксис сверялся с [документацией MikroTik](https://help.mikrotik.com/docs/spaces/ROS/pages/84901929/Container); набор параметров зависит от версии RouterOS.

Разместите `internal.secret` в отдельном каталоге `/Containers/remote-control-relay-secrets` и обеспечьте доступ процессу контейнера UID 65532. Не монтируйте туда всю серверную `/data`.

```routeros
/container/mounts/add list=RCR_SECRETS src=/Containers/remote-control-relay-secrets dst=/run/secrets
/container/envs/add list=RCR_ENV key=RDS_HBBR_ID value=relay-eu-01
/container/envs/add list=RCR_ENV key=RDS_HBBR_NAME value="Europe relay"
/container/envs/add list=RCR_ENV key=RDS_HBBR_REGION value=eu
/container/envs/add list=RCR_ENV key=RDS_HBBR_PUBLIC_ADDRESS value="relay.example.net:21117"
/container/envs/add list=RCR_ENV key=RDS_API_INTERNAL_URL value="http://10.20.0.1:21114"
/container/add file=remote-control-relay-2.2.1-amd64.tar name=remote_control_relay interface=veth-relay root-dir=/Containers/remote_control_relay mountlists=RCR_SECRETS envlist=RCR_ENV start-on-boot=yes logging=yes
```

Подставьте реальные адреса и готовый `veth-relay`. Firewall должен разрешать публичные TCP 21117/21119 и UDP 21119 только от приватных адресов API/HBBS. Шаблон не запускался на отдельном production-узле; для фактической установки нужен адрес выбранного узла.

## Ограничения и проверки

- Совместимость с управляющим протоколом нашего сервера; не drop-in замена hbbr для произвольного upstream RustDesk Pro/OSS.
- При перезапуске активные relay-сеансы обрываются; долговременных данных нет, достаточно повторно подключить файл секрета.
- Падение API не должно прерывать уже соединённые пары; новые пары требуют permit HBBS. Телеметрия lifecycle не имеет надёжной очереди на диске.
- Нет встроенного shell-healthcheck. Проверяйте телеметрию в консоли и функциональный smoke-тест, одного открытого TCP-порта недостаточно.
- Локальный протокольный тест: `tests/probe.mjs` (Node.js нужен только для тестов, отсутствует в образе).

## Проверенная поставка 2026-09-07

Compose-конфигурация прошла `config --quiet` с `.env.example`. Тестовые контейнеры и сеть после проверки удалены.

| Архитектура | Бинарник | Tar-архив | SHA-256 архива |
|---|---:|---:|---|
| amd64 | 2 309 440 B | 1 077 248 B | `6b5701293cfa3ab13bac66290572f8c0bb3f26ae893cbdbf126aef252d2c0979` |
| arm64 | 2 038 560 B | 1 039 360 B | `6a2710bde20682c984241ea7b48c03de777c9e8be8e86f9d6c11776cbac929fa` |

В каждом образе проверены один слой и единственный файл `art-hbbr`, правильная ELF-архитектура, UID/GID 65532. Local image index: `sha256:fa970ea36991f6176c140106f750c125844d66e53230a96d002c0582e9095eca`.

Оба образа прошли реальные двунаправленные TCP/TCP, WS/WS и TCP/WS тесты, отказ без permit, отказ с неверным токеном и по истечению разрешения, запрет третьего peer, terminate/ack с закрытием обоих концов и регистрацию телеметрии в отдельном API 2.2.1. Контейнеры работали read-only с cap-drop ALL. [Машиночитаемый отчёт](tests/verification-2026-09-07.json).

Замер Docker stats после smoke: amd64 около 1.54 MiB; arm64 около 16.93 MiB **под QEMU**, поэтому последнее число не характеризует нативный ARM64. Это не нагрузочный benchmark. Официальный клиент и отдельный MikroTik-узел в этой проверке не использовались.

Повторение на Windows с Docker Desktop: `./tests/smoke.ps1 -Docker <путь-к-docker.exe>`. Скрипт создаёт временную Docker-сеть, API, по очереди оба relay и Node-пробу, затем удаляет тестовые контейнеры/сеть/секрет. Требуются локально собранный image и `node:24-alpine`. Порты 23414, 23417, 23419 должны быть свободны. Протокольные пробы идут внутри Docker-сети; ранняя проверка через Windows published TCP port зависала до EOF и не использована как доказательство ошибки HBBR.

Экспорт после сборки:

```sh
mkdir -p outputs
docker image save --platform linux/amd64 -o outputs/remote-control-relay-2.2.1-amd64.tar remote-control-relay:2.2.1
docker image save --platform linux/arm64 -o outputs/remote-control-relay-2.2.1-arm64.tar remote-control-relay:2.2.1
```
