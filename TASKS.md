# TASKS.md — Atomic implementation plan

Этот файл предназначен для пошаговой реализации проекта через coding agent/LLM. Каждая задача должна выполняться отдельно. Не объединять соседние задачи «для удобства», даже если они кажутся маленькими. После каждой задачи код должен собираться, а существующие тесты должны проходить.

Главное правило: одна задача — одна понятная цель. Если задача начинает требовать изменения большого числа несвязанных компонентов, её нужно дополнительно разбить.

## Phase 0 — Skeleton

### T001 — Создать Go module

Создать `go.mod`.

Добавить минимальный `cmd/gateway/main.go`.

Программа должна запускаться и завершаться без ошибки.

Acceptance:

`go build ./...` проходит.

Никакой HTTP логики пока не добавлять.

### T002 — Добавить базовую структуру директорий

Создать:

`internal/config`

`internal/httpserver`

`internal/proxy`

`internal/transport`

`internal/streaming`

Не создавать пустые interfaces и boilerplate-файлы только ради структуры.

Acceptance:

проект собирается.

### T003 — Добавить минимальный Config type

Создать `internal/config/config.go`.

Поддержать только:

- listen address;
- upstream base URL;
- upstream API key.

Не добавлять SQLite, limits, pricing и logging config.

Acceptance:

config можно создать в коде и проверить unit test.

### T004 — Загрузка YAML config

Добавить загрузку config из YAML-файла.

Путь передаётся через CLI flag:

`--config`

Acceptance:

валидный config загружается;

невалидный YAML возвращает понятную ошибку;

отсутствующий обязательный upstream URL вызывает ошибку.

### T005 — Environment substitution для upstream key

Разрешить upstream API key задавать через environment variable.

Не делать универсальный template engine.

Acceptance:

secret можно получить из env;

если переменная отсутствует — startup error.

## Phase 1 — HTTP server

### T006 — Поднять HTTP server

Добавить HTTP server на configured address.

Пока достаточно одного handler.

Acceptance:

server запускается;

можно сделать HTTP request;

graceful shutdown пока не нужен.

### T007 — Добавить `/health`

Реализовать:

`GET /health`

Ответ:

HTTP 200.

Никаких upstream checks.

Acceptance:

integration test получает 200.

### T008 — Добавить request ID

На каждый входящий request создавать уникальный request ID.

Добавлять:

`X-Gateway-Request-ID`

в response.

Acceptance:

два запроса получают разные ID.

### T009 — Добавить базовый structured request log

Логировать:

- request ID;
- method;
- path;
- status;
- duration.

Не логировать body.

Не логировать Authorization.

Acceptance:

один request создаёт один completion log.

## Phase 2 — Raw reverse proxy

### T010 — Создать upstream HTTP client

Добавить один reusable `http.Client`.

Не создавать client на каждый request.

Acceptance:

client создаётся один раз при startup.

### T011 — Проксировать method/path/query

Любой `/v1/*` request должен идти на configured upstream с теми же:

- method;
- path;
- query parameters.

Acceptance:

mock upstream видит идентичный method/path/query.

### T012 — Проксировать request body

Передавать body upstream без изменения.

Пока не читать и не парсить JSON.

Acceptance:

mock upstream получает byte-identical body.

### T013 — Переписывать Authorization

Client Authorization не должен уходить в 9router.

Upstream должен получать configured upstream API key.

Acceptance:

mock upstream видит upstream key;

client key отсутствует.

### T014 — Копировать безопасные request headers

Копировать обычные end-to-end headers.

Не копировать hop-by-hop headers.

Acceptance:

`Content-Type` сохраняется;

`Connection` не проксируется.

### T015 — Проксировать status code

Upstream HTTP status должен возвращаться клиенту без изменения.

Acceptance:

upstream 418 → client 418.

### T016 — Проксировать response headers

Копировать end-to-end upstream response headers.

Не копировать hop-by-hop headers.

Acceptance:

upstream custom response header появляется у клиента.

### T017 — Проксировать обычный response body

Для non-stream response просто передавать bytes клиенту.

Не выполнять JSON unmarshal.

Acceptance:

JSON body проходит byte-identical.

### T018 — Unknown `/v1/*` passthrough

Не ограничивать proxy только `/chat/completions`.

Любой `/v1/...` должен proxy'ться.

Acceptance:

`POST /v1/something-unknown` доходит до mock upstream.

## Phase 3 — Cancellation

### T019 — Привязать upstream request к client context

Upstream request должен использовать context входящего request.

Acceptance:

client cancellation отменяет context upstream handler.

### T020 — Добавить тест client cancellation

Mock upstream должен ждать cancellation.

Client запускает request и отменяет context.

Acceptance:

upstream замечает cancellation без длительного timeout.

## Phase 4 — Transparent streaming

### T021 — Определять `text/event-stream`

После получения upstream headers определить SSE по `Content-Type`.

Не использовать поле `stream` request для этого решения.

Acceptance:

`text/event-stream; charset=utf-8` определяется как SSE.

### T022 — Добавить SSE passthrough path

Для SSE response читать upstream body и сразу писать downstream.

Не парсить SSE.

Acceptance:

stream доходит до клиента.

### T023 — Flush после streaming write

После полученного streaming fragment выполнять downstream Flush.

Acceptance:

первый chunk доступен клиенту до завершения всего response.

### T024 — Добавить mock streaming endpoint

Mock upstream должен уметь:

- отправить chunk;
- Flush;
- подождать;
- отправить второй chunk.

Acceptance:

integration test подтверждает реальный incremental delivery.

### T025 — EOF должен закрывать downstream сразу

После upstream EOF handler должен завершаться немедленно.

Не ждать `[DONE]`.

Acceptance:

SSE без `[DONE]` корректно заканчивается через EOF.

### T026 — Regression test Bifrost end-delay

Mock upstream:

1. отправляет terminal chunk;
2. Flush;
3. закрывает body.

Измерить downstream completion.

Acceptance:

gateway не добавляет искусственную задержку более разумного CI threshold.

Использовать generous threshold, например 250 ms, а не несколько миллисекунд.

### T027 — SSE `[DONE]` passthrough

Если upstream прислал:

`data: [DONE]`

передать его без изменения.

Acceptance:

client получает `[DONE]`.

### T028 — Heartbeat passthrough

SSE comments вроде:

`: heartbeat`

должны проходить без изменения.

Gateway сам heartbeat не генерирует.

Acceptance:

mock heartbeat доходит до клиента.

## Phase 5 — Concurrency transport tests

### T029 — Добавить тест двух параллельных requests

Mock upstream использует barrier.

Два requests должны одновременно достичь upstream.

Acceptance:

второй request не ждёт окончания первого.

### T030 — Проверить connection pooling

Добавить test/instrumentation, подтверждающий reuse одного transport/client.

Не оптимизировать pool settings пока нет проблемы.

Acceptance:

код не создаёт `http.Client` per request.

## Phase 6 — Response classifier

### T031 — Создать ResponseMode

Добавить:

- JSON;
- SSE;
- Opaque.

Только классификация.

Acceptance:

unit tests для типичных Content-Type.

### T032 — JSON Content-Type detection

Поддержать:

`application/json`

и `application/*+json`.

Acceptance:

оба определяются как JSON.

### T033 — Opaque response mode

Любой другой Content-Type считать opaque.

Opaque body должен proxy'ться без изменения.

Acceptance:

binary response проходит byte-identical.

## Phase 7 — Minimal OpenAI request inspection

### T034 — Создать request metadata struct

Поля только:

- model;
- stream;
- max_tokens/max_completion_tokens при наличии.

Не создавать полную OpenAI request schema.

### T035 — Парсить `model`

Для `/v1/chat/completions` извлечь model из JSON body.

Acceptance:

model читается;

unknown JSON fields не мешают.

### T036 — Парсить `stream`

Извлечь optional boolean `stream`.

Если поля нет — сохранить unknown/default отдельно.

Acceptance:

true/false/absence различаются.

### T037 — Парсить output token limit

Поддержать известные поля:

- `max_tokens`;
- `max_completion_tokens`.

Не пытаться нормализовать весь request.

Acceptance:

значение извлекается при наличии.

### T038 — Восстанавливать исходный request body

После inspection upstream должен получить исходные raw bytes, а не JSON re-marshaled representation.

Acceptance:

body до и после inspection byte-identical.

## Phase 8 — Generic SSE parser

### T039 — Создать `SSEEvent`

Структура:

- Event;
- Data.

Никакой OpenAI логики.

### T040 — Парсить один SSE event

Поддержать:

`data: ...`

и завершение event пустой строкой.

Acceptance:

unit test.

### T041 — Поддержать `event:`

Парсить optional event name.

Acceptance:

Anthropic-like SSE синтаксически читается, даже если gateway его не понимает семантически.

### T042 — Поддержать comments

Строки, начинающиеся с `:`, не считать data.

Acceptance:

`: heartbeat` не ломает parser.

### T043 — Поддержать split TCP reads

SSE строка должна корректно собираться из нескольких `Read()`.

Acceptance:

`da` + `ta: {...}` распознаётся.

### T044 — Поддержать несколько events в одном read

Acceptance:

три event подряд возвращаются как три события.

### T045 — Ограничить максимальный размер SSE event

Добавить configurable/internal safety limit.

Acceptance:

слишком большой event возвращает controlled error, а не бесконечно растит память.

## Phase 9 — OpenAI SSE observation

### T046 — Создать OpenAI stream observer

Observer принимает готовые `SSEEvent`.

Пока только пытается parse JSON.

Transport не зависит от результата parsing.

### T047 — Извлекать response ID и model

Acceptance:

metadata заполняется из первого chunk.

### T048 — Извлекать finish_reason

Acceptance:

terminal reason сохраняется.

Не закрывать transport на основании finish_reason.

### T049 — Извлекать usage

Поддержать:

- prompt/input tokens;
- completion/output tokens;
- total tokens.

Acceptance:

usage извлекается из terminal chunk.

### T050 — `[DONE]` detection

Observer умеет распознать `[DONE]`.

Это metadata, не transport requirement.

### T051 — Parser error не должен ломать passthrough

Если один chunk содержит неизвестный/невалидный JSON, transparent stream должен продолжаться.

Acceptance:

client получает все raw bytes несмотря на observer error.

## Phase 10 — SSE to non-stream conversion

### T052 — Определить mismatch condition

Если client request имеет `stream:false`, но upstream вернул SSE, выбрать aggregation path.

Acceptance:

этот сценарий не попадает в raw SSE passthrough.

### T053 — Создать basic accumulator

Accumulator хранит:

- id;
- model;
- created;
- content;
- finish_reason.

Только один choice сначала.

### T054 — Собирать content deltas

Последовательные:

`"content":"TEST"`

и:

`"content":"_OK"`

должны стать:

`TEST_OK`.

Acceptance:

unit test.

### T055 — Собирать role

Role из первого delta должен попасть в итоговое `message.role`.

### T056 — Собирать finish_reason

Terminal finish reason должен попасть в итоговый JSON.

### T057 — Собирать usage

Последний известный usage должен попасть в итоговый JSON.

### T058 — Завершать aggregation по `[DONE]`

Acceptance:

возвращается итоговый JSON.

### T059 — Завершать aggregation по EOF без `[DONE]`

Это обязательный regression case 9router.

Acceptance:

terminal chunks + EOF дают HTTP 200 JSON.

### T060 — Regression test Bifrost unmarshal bug

Client:

`stream:false`

Upstream:

`Content-Type: text/event-stream`

и SSE body.

Acceptance:

gateway возвращает valid JSON, а не `failed to unmarshal`.

## Phase 11 — Tool calls aggregation

### T061 — Добавить tool call storage

Поддержать `choices[].delta.tool_calls`.

Пока только один choice.

### T062 — Собирать tool call ID

ID может прийти только в одном из chunks.

Acceptance:

не теряется.

### T063 — Собирать function name

Acceptance:

function name сохраняется.

### T064 — Конкатенировать function arguments

Arguments приходят частями.

Не JSON-decode каждый fragment.

Acceptance:

`{"pa` + `th":"x"}` → `{"path":"x"}`.

### T065 — Поддержать несколько tool calls

Использовать upstream tool call index.

Acceptance:

два tool calls не смешиваются.

### T066 — Добавить multiple choices support

Только после tool call aggregation для одного choice.

Acceptance:

choice 0 и choice 1 собираются независимо.

## Phase 12 — API key storage

### T067 — Подключить SQLite

Добавить SQLite driver.

Открыть DB по configured path.

Пока без schema.

### T068 — Включить WAL и foreign keys

Acceptance:

startup выполняет необходимые PRAGMA.

### T069 — Добавить migration mechanism

Использовать встроенные SQL migrations.

Acceptance:

пустая DB автоматически обновляется.

### T070 — Создать `api_keys` table

Поля согласно `ARCHITECTURE.md`.

Не добавлять usage tables пока.

### T071 — Создать API key generator

Генерировать cryptographically secure random key.

Acceptance:

keys имеют достаточно entropy.

### T072 — Реализовать key hashing

Не хранить raw key.

Acceptance:

DB содержит только hash/prefix.

### T073 — Создать API key repository

Методы:

- create;
- get by hash/prefix;
- list;
- disable.

Никаких limits пока.

### T074 — Добавить static admin CLI command `key create`

Можно временно реализовать через локальный server subcommand или repository access.

Выводить raw key один раз.

### T075 — Добавить key authentication middleware

Каждый `/v1/*` request требует gateway key.

Acceptance:

валидный key проходит;

невалидный → 401;

upstream при 401 не вызывается.

### T076 — Disabled key rejection

Acceptance:

disabled key → controlled error до upstream.

## Phase 13 — In-memory key cache

### T077 — Загружать keys в memory при startup

Authentication не должен выполнять SQL query каждый request.

### T078 — Lookup из memory cache

Acceptance:

hot path не обращается в SQLite.

### T079 — Обновлять cache после disable/create

Acceptance:

restart не требуется для изменения key.

## Phase 14 — Model policy

### T080 — Добавить allow models в policy JSON

Только exact match сначала.

### T081 — Reject forbidden model

Acceptance:

HTTP request не достигает upstream.

### T082 — Добавить glob support

Например:

`kr/*`.

Не добавлять regex.

### T083 — Добавить deny list

Deny должен иметь приоритет над allow.

Acceptance:

явно запрещённая модель блокируется.

## Phase 15 — Concurrency limiter

### T084 — Добавить `max_concurrent_requests` в key policy

0 означает unlimited.

### T085 — Реализовать per-key semaphore

Acceptance:

при limit=2 два requests проходят одновременно.

### T086 — Reject сверх concurrency

Не ставить в очередь в MVP.

Вернуть 429.

Acceptance:

третий request rejected сразу.

### T087 — Release slot после normal completion

Acceptance:

новый request проходит после завершения предыдущего.

### T088 — Release slot после upstream error

Acceptance:

slot не теряется.

### T089 — Release slot после client cancellation

Acceptance:

slot не теряется.

## Phase 16 — Request rate limits

### T090 — Добавить простой RPM policy

Начать только с requests/minute.

Не делать generic arbitrary windows сразу.

### T091 — Реализовать fixed/sliding minute counter

Выбрать одну semantics и документировать.

### T092 — Reject RPM exceeded

Вернуть 429.

### T093 — Добавить `Retry-After`

Acceptance:

header присутствует при известном reset time.

### T094 — Тест rollover minute window

Использовать fake clock.

Не ждать реальную минуту.

## Phase 17 — Generic request windows

Только после стабильного RPM.

### T095 — Вынести request window type

Поддержать amount + duration.

### T096 — Добавить hour window

Acceptance:

requests/hour работает независимо от RPM.

### T097 — Поддержать несколько request windows одновременно

Request должен пройти все configured windows.

## Phase 18 — Usage accounting

### T098 — Создать internal Usage type

Поля:

- input;
- output;
- total.

Не добавлять pricing.

### T099 — JSON response usage parser

Для non-stream OpenAI response извлечь usage.

### T100 — SSE usage connection

Использовать уже существующий stream observer.

### T101 — Создать request result object

После completion собрать:

- usage;
- status;
- termination reason;
- duration.

### T102 — Не считать отсутствующий usage ошибкой

Unknown usage допустим.

Acceptance:

request успешно заканчивается.

## Phase 19 — Token estimation

### T103 — Создать `TokenEstimator` interface

Без implementation.

### T104 — Создать naive estimator

Сделать простую документированную heuristic.

Не выдавать её за exact tokenizer.

### T105 — Извлекать текст из messages

Estimator считает только known textual fields.

### T106 — Учитывать tool schemas приблизительно

Не игнорировать большой `tools` JSON полностью.

### T107 — Добавить estimate confidence

Например:

- low;
- medium;
- exact.

## Phase 20 — Token limits

### T108 — Добавить TPM policy

Только один minute token window сначала.

### T109 — Создать token reservation

До upstream request зарезервировать estimated usage.

### T110 — Учитывать max output reservation

Использовать `max_tokens`/`max_completion_tokens` если они есть.

### T111 — Reject reservation, превышающий TPM

Acceptance:

upstream не вызывается.

### T112 — Reconcile reservation с actual usage

После response:

reservation удаляется;

actual usage фиксируется.

### T113 — Refund unused reservation

Acceptance:

резерв 1000, actual 100 → 900 становится доступно снова.

### T114 — Concurrent reservation test

Два запроса вместе не должны oversubscribe TPM.

### T115 — Handling unknown actual usage

Выбрать conservative policy и задокументировать.

Не оставлять reservation навечно.

## Phase 21 — Generic token windows

### T116 — Вынести token window abstraction

Amount + duration.

### T117 — Добавить tokens/hour

### T118 — Добавить tokens/day

### T119 — Несколько token windows одновременно

Request должен пройти каждое ограничение.

## Phase 22 — RequestLease

### T120 — Создать RequestLease

Объединить:

- concurrency slot;
- token reservation.

Budget пока не добавлять.

### T121 — Реализовать `Commit`

Idempotent.

### T122 — Реализовать `Abort`

Idempotent.

### T123 — Заменить разрозненные releases на RequestLease

Acceptance:

в основных request paths больше нет ручного release нескольких ресурсов.

## Phase 23 — Pricing

### T124 — Создать Money type в integer micros

Не использовать float64.

### T125 — Создать pricing rule struct

Input/output price per million tokens.

### T126 — Pricing exact model lookup

Только exact сначала.

### T127 — Добавить glob pricing rules

### T128 — Рассчитывать actual request cost

Только если usage известен.

### T129 — Unknown pricing state

Отсутствие цены не должно превращаться в `$0` без пометки.

## Phase 24 — Budget

### T130 — Добавить total budget policy

Начать только с lifetime/total budget.

Не делать month/day сразу.

### T131 — Хранить spent amount

Persistence можно добавить после basic implementation.

### T132 — Создать budget reservation

Использовать estimated tokens + pricing.

### T133 — Reject при недостаточном доступном budget

### T134 — Reconcile budget reservation

Estimate заменяется actual cost.

### T135 — Concurrent budget test

Два параллельных request не должны вместе превысить budget.

### T136 — Добавить daily budget

Только после total budget.

### T137 — Добавить monthly budget

Использовать календарный месяц.

Не считать месяц равным 30 дням.

## Phase 25 — Request history

### T138 — Создать `requests` migration

Только metadata.

Body отдельно.

### T139 — Создать RequestRecord type

Immutable completion record.

### T140 — Добавить telemetry queue

Bounded channel.

### T141 — Добавить telemetry writer goroutine

Писать RequestRecord в SQLite.

### T142 — Streaming не должен ждать SQLite

Добавить integration test с искусственно медленным writer.

### T143 — Записывать completed requests

### T144 — Записывать rejected requests

Например rate limit/budget.

### T145 — Записывать cancelled requests

## Phase 26 — Request body capture

### T146 — Добавить body logging config flag

Default false.

### T147 — Реализовать bounded client body capture

Сохранять максимум N bytes.

### T148 — Хранить original body size

### T149 — Добавить `request_bodies` table

### T150 — Записывать captured request body

Только если logging enabled.

### T151 — Не записывать Authorization

Acceptance:

secret отсутствует в DB.

### T152 — Truncation metadata

UI/admin API должен в будущем видеть, что body обрезан.

## Phase 27 — Response body capture

Не обязательно включать по умолчанию.

### T153 — Добавить independent response body flag

### T154 — Bounded JSON response capture

### T155 — Streaming capture без блокировки

Если streaming body logging включён, recorder не должен менять Flush behavior.

### T156 — Ограничить размер captured stream

Не хранить бесконечный response.

## Phase 28 — Latency telemetry

### T157 — Измерять upstream headers latency

### T158 — Измерять TTFT

Первый реально отправленный downstream response byte/chunk.

### T159 — Измерять total duration

### T160 — Измерять last meaningful stream event

Использовать observer metadata.

### T161 — Вычислять stream close delay

Не использовать эту метрику для transport control.

### T162 — Regression assertion для close delay

Mock upstream EOF → gateway close без секундной задержки.

## Phase 29 — Error model

### T163 — Создать GatewayError

HTTP status + code + safe message + internal cause.

### T164 — OpenAI-style error renderer

### T165 — `invalid_api_key`

### T166 — `model_not_allowed`

### T167 — `request_rate_limit_exceeded`

### T168 — `concurrency_limit_exceeded`

### T169 — `token_limit_exceeded`

### T170 — `budget_exceeded`

### T171 — Upstream errors остаются upstream errors

Не переписывать валидный upstream JSON error без необходимости.

## Phase 30 — Readiness

### T172 — Добавить `/ready`

### T173 — Readiness false при недоступной DB

### T174 — Readiness false до завершения migrations

### T175 — Не проверять LLM generation в `/ready`

## Phase 31 — Admin API

### T176 — Добавить admin auth token

Отдельный secret.

### T177 — `GET /admin/v1/keys`

### T178 — `POST /admin/v1/keys`

### T179 — `GET /admin/v1/keys/{id}`

### T180 — `PATCH /admin/v1/keys/{id}`

### T181 — Disable/revoke endpoint

### T182 — `GET /admin/v1/requests`

### T183 — Pagination request list

Не возвращать всю таблицу.

### T184 — `GET /admin/v1/requests/{id}`

### T185 — Request body retrieval

Только при наличии captured body.

## Phase 32 — CLI

### T186 — Создать `gwctl`

### T187 — Admin endpoint configuration

### T188 — `gwctl key list`

### T189 — `gwctl key create`

### T190 — `gwctl key disable`

### T191 — `gwctl request list`

### T192 — `gwctl request show`

Не добавлять interactive TUI.

## Phase 33 — Prometheus

### T193 — Добавить `/metrics`

### T194 — Requests total

### T195 — Active requests gauge

### T196 — Request duration histogram

### T197 — TTFT histogram

### T198 — Stream close delay histogram

### T199 — Token counters

### T200 — Rejection counters

Не использовать request ID как label.

## Phase 34 — Graceful shutdown

### T201 — Обрабатывать SIGTERM/SIGINT

### T202 — Перестать принимать новые requests

### T203 — Дождаться active requests ограниченное время

### T204 — Cancel оставшиеся requests

### T205 — Flush telemetry queue

С bounded timeout.

### T206 — Close SQLite

## Phase 35 — Docker

### T207 — Multi-stage Dockerfile

### T208 — Запуск от non-root user

### T209 — `/data` для SQLite

### T210 — Example `docker-compose.yml`

Только один service gateway.

9router считается внешним/существующим service.

### T211 — Docker healthcheck

Использовать `/health`.

## Phase 36 — Security cleanup

### T212 — Запрет arbitrary upstream URL

Client не может менять host.

### T213 — Защитить path joining

Проверить unusual paths и `//host`.

### T214 — Request body size limit

### T215 — Admin routes не доступны с обычным API key

### T216 — Secret redaction tests

Authorization/upstream key не появляются в logs/history.

## Phase 37 — Performance tests

### T217 — Direct mock baseline

Измерить direct upstream latency.

### T218 — Gateway passthrough benchmark

### T219 — Streaming TTFT comparison

### T220 — Streaming close comparison

### T221 — Parallel request benchmark

Не оптимизировать код до появления результатов.

## Phase 38 — Real 9router verification

### T222 — Реальный `stream:true` smoke test

Gateway → 9router.

### T223 — Сравнить TTFT direct vs gateway

### T224 — Реальный `stream:false` test

### T225 — Force-stream compatibility test

Использовать provider 9router, который фактически force-stream'ит.

Acceptance:

client получает обычный JSON.

### T226 — OpenCode smoke test

OpenCode работает через gateway.

### T227 — OpenCode parallel requests test

Title/main generation не сериализуются gateway.

### T228 — OpenCode completion delay test

После окончания model response OpenCode не ждёт дополнительные ~6 секунд из-за gateway.

Это ключевой release blocker.

## Phase 39 — Documentation

### T229 — README basic setup

### T230 — Config example

### T231 — API key example

### T232 — Limits examples

### T233 — Streaming behavior documentation

Обязательно описать forced-stream compatibility.

### T234 — Privacy/body logging warning

### T235 — THIRD_PARTY_NOTICES

Добавить до копирования/адаптации Bifrost code.

## Phase 40 — Only after MVP

Эти задачи не выполнять, пока T001–T235 не дают стабильный usable gateway.

### T236 — Web UI skeleton

### T237 — Dashboard

### T238 — Keys page

### T239 — Requests page

### T240 — Request inspector

### T241 — Usage charts

### T242 — Pricing editor

Не добавлять multi-upstream routing, provider translation, Anthropic API translation или plugin system без отдельного изменения product scope.

## Правила работы coding agent

Перед каждой задачей прочитать `PLAN.md`, `ARCHITECTURE.md` и текущую задачу.

Не реализовывать будущие задачи заранее.

Не менять public behavior соседнего компонента без необходимости.

Если для выполнения задачи требуется крупный refactor, сначала остановиться и разбить его на отдельные задачи.

После каждой задачи выполнить минимум:

`go fmt ./...`

`go test ./...`

`go build ./...`

Если добавляется HTTP/streaming behavior — добавить соответствующий test в той же задаче.

Не оставлять сломанные тесты с объяснением «будет исправлено следующей задачей».

Не добавлять abstraction, если она пока имеет одну очевидную реализацию и не нужна для тестирования.

Не использовать TODO как замену реализации acceptance criteria текущей задачи.

Не расширять scope задачи по собственной инициативе.

При конфликте между удобством implementation и прозрачностью transport приоритет имеет прозрачность transport.

Главные release blockers на всём протяжении разработки:

1. Gateway не должен добавлять заметный TTFT.
2. Gateway не должен задерживать завершение SSE.
3. Gateway не должен сериализовать requests без configured limit.
4. `stream:false` не должен ломаться, если upstream force-stream'ит.
5. Cancellation клиента должен прекращать upstream generation.
6. Limiter/accounting/logging не должны превращать proxy hot path в тяжёлую processing pipeline.
