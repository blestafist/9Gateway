# ARCHITECTURE.md — Lightweight LLM Gateway / Limiter for 9router

## 1. Цель документа

Этот документ определяет техническую архитектуру gateway, описанного в `PLAN.md`. Он должен использоваться как основной ориентир при реализации: какие пакеты существуют, кто за что отвечает, какие интерфейсы считаются стабильными, как проходит request lifecycle, как устроены streaming и accounting, что хранится в SQLite и в каком порядке следует реализовывать систему.

Ключевое ограничение архитектуры: transport path должен оставаться максимально простым. Auth, limiter, tokenizer, accounting, logging и observability могут наблюдать запрос, резервировать ресурсы или остановить его, но не должны без необходимости преобразовывать HTTP payload и особенно не должны влиять на скорость streaming passthrough.

Основной runtime flow:

`Client → HTTP Server → Auth → Request Metadata → Policy Check → Reservation → Upstream Transport → 9router → Response Transport → Accounting → Client`

Для обычного streaming response фактический hot path должен быть ещё проще:

`9router response body → read → write client → flush`

Параллельно с этим допускается пассивное наблюдение за передаваемыми данными для извлечения usage и telemetry.

## 2. Основные архитектурные принципы

Первый принцип — transport и protocol semantics разделены. HTTP transport отвечает за передачу байтов, cancellation, headers, timeouts и connection lifecycle. OpenAI parser отвечает за извлечение `model`, `stream`, `usage`, `finish_reason`, `tool_calls` и других известных полей. Parser не должен управлять временем жизни transport в обычном passthrough режиме.

Второй принцип — response определяется фактическим upstream response. Нельзя предполагать, что `stream:false` означает JSON response. Если upstream вернул `Content-Type: text/event-stream`, это SSE независимо от параметра исходного запроса.

Третий принцип — accounting не должен находиться на критическом пути передачи каждого токена. Данные могут проходить через лёгкий observer/tee, но SQLite, сложный tokenizer, pricing lookup или тяжёлая агрегация не должны выполняться синхронно перед каждым downstream Flush.

Четвёртый принцип — reservation до request, reconciliation после request. Concurrency, token limits и budgets должны учитывать уже выполняющиеся запросы, а не только завершённый usage.

Пятый принцип — single-instance first. Первая архитектура оптимизирована под один gateway process. Runtime coordination выполняется в RAM, persistence — SQLite. Distributed locking, Redis и multi-node consistency отсутствуют до появления реальной необходимости.

Шестой принцип — unknown API is valid API. Если endpoint не требует специального понимания gateway, он должен прозрачно проксироваться.

## 3. Структура репозитория

Предлагаемая структура:

`cmd/gateway/` — entry point основного server binary.

`cmd/gwctl/` — CLI администратора. Можно добавить позже; на первых этапах допустимо объединить CLI и server в один binary с subcommands.

`internal/app/` — сборка зависимостей и lifecycle приложения.

`internal/config/` — загрузка YAML/env configuration и validation.

`internal/httpserver/` — маршрутизация HTTP endpoint'ов, middleware chain, health/admin endpoints.

`internal/proxy/` — основной reverse proxy orchestration.

`internal/transport/` — upstream HTTP transport, headers, connection settings, cancellation, timeout configuration.

`internal/protocol/openai/` — минимальное понимание OpenAI-compatible request/response.

`internal/streaming/` — SSE parser, transparent passthrough observer, SSE accumulator.

`internal/auth/` — API key parsing, hashing, lookup и authentication middleware.

`internal/policy/` — сбор effective policy для API key/request/model.

`internal/limiter/` — request windows, token windows, concurrency и reservations.

`internal/accounting/` — usage, budget, pricing и reconciliation.

`internal/tokenizer/` — token estimator/tokenizer abstraction.

`internal/storage/` — SQLite implementation и migrations.

`internal/observability/` — request trace, metrics, body capture, structured logging.

`internal/admin/` — admin service/API.

`internal/model/` — небольшое количество shared domain structures, если действительно необходимо.

`internal/testupstream/` или `test/mockupstream/` — mock 9router/OpenAI upstream для integration tests.

Не создавать generic `utils`, `helpers`, `common` как свалку. Если функция относится к SSE, она живёт в streaming; если к API key — в auth.

## 4. Dependency direction

Зависимости должны преимущественно идти от orchestration к маленьким интерфейсам:

`httpserver → proxy`

`proxy → auth/policy/limiter/transport/accounting/observability`

`accounting → tokenizer/storage`

`admin → storage/policy`

`streaming → protocol/openai` допустимо для OpenAI-specific accumulator, но generic SSE reader не должен зависеть от OpenAI.

Storage implementation зависит от domain structures, но domain logic не должна зависеть от SQLite-specific типов.

Нельзя допускать:

`storage → proxy`

`tokenizer → httpserver`

`accounting → transport`

`limiter → streaming`

`protocol/openai → SQLite`

Transport не должен знать о budget.

## 5. RequestContext

Для каждого входящего запроса создаётся внутренний объект request context. Не следует путать его с `context.Context`.

Пример структуры:

`RequestID string`

`StartedAt time.Time`

`APIKeyID int64`

`APIKeyName string`

`Method string`

`Path string`

`Model string`

`ClientRequestedStream *bool`

`RequestContentType string`

`RequestBytes int64`

`EstimatedInputTokens int64`

`ReservedTokens int64`

`ReservedCostMicros int64`

`Policy EffectivePolicy`

`Trace *RequestTrace`

Большая часть полей metadata. Сам request body не должен без необходимости храниться здесь целиком.

Для request ID предпочтительно использовать UUID/ULID либо другой collision-safe identifier.

## 6. Middleware chain

HTTP request должен проходить примерно следующий pipeline.

Сначала создаётся RequestID и базовый trace. Затем проверяется размер request body на абсолютный технический максимум. После этого выполняется authentication API key. Далее извлекается минимальная OpenAI metadata: endpoint, model, requested stream mode и некоторые параметры, необходимые policy engine.

После этого policy service собирает EffectivePolicy: глобальные defaults + policy API key + model-specific ограничения, если они будут поддерживаться.

Далее выполняются preflight checks в следующем порядке: key enabled/expiry, model allow/deny, RPM/request windows, concurrency, preliminary token reservation, preliminary budget reservation.

Если любой check не проходит, upstream request вообще не создаётся.

После успешных checks request передаётся proxy service.

Все захваченные ресурсы должны освобождаться через единый RequestLease/Reservation объект, а не через разрозненные `defer` в пяти middleware.

## 7. RequestLease

Нужен центральный объект, представляющий ресурсы, зарезервированные для выполняющегося request.

Концептуальный интерфейс:

```go
type RequestLease interface {
    ReservedTokens() int64
    ReservedCostMicros() int64
    Commit(ctx context.Context, result UsageResult) error
    Abort(ctx context.Context, result PartialUsageResult) error
}
```

Lease внутри может содержать concurrency slot, token reservation, budget reservation и request-window consumption.

`Commit` вызывается при нормальном завершении.

`Abort` вызывается при client cancellation, upstream failure или forced termination.

Обе операции должны быть idempotent: случайный повторный вызов не должен дважды возвращать reservation или дважды списывать usage.

Concurrency slot освобождается и при Commit, и при Abort.

## 8. ProxyService

Основной orchestration interface:

```go
type ProxyService interface {
    ServeHTTP(w http.ResponseWriter, r *http.Request, req *RequestContext)
}
```

Внутренне proxy определяет response strategy только после получения upstream headers.

Логика:

1. Создать upstream request на тот же method/path/query.
2. Привязать его к client context.
3. Заменить gateway Authorization на upstream 9router credential.
4. Удалить hop-by-hop headers.
5. Отправить upstream.
6. Получить response headers.
7. Определить фактический response mode.
8. Выбрать `passthroughJSON`, `passthroughSSE` или `aggregateSSEToJSON`.
9. После завершения выполнить accounting reconciliation.
10. Завершить request trace.

Нельзя выбирать response parser до получения upstream `Content-Type`.

## 9. Request body handling

Есть конфликт между transparency, token estimation и body inspection: tokenizer и parser хотят прочитать JSON body, но upstream тоже должен получить его без изменений.

Для OpenAI-known endpoints допустимо прочитать request body один раз в bounded buffer, если размер находится в разумном configurable limit. После чтения создаётся новый `io.Reader` для upstream.

Это позволяет извлечь model, stream, max_tokens и оценить prompt tokens без сложного streaming JSON parser.

Но generic passthrough endpoint не обязан полностью буферизоваться. Для неизвестных endpoints предпочтительно streaming body proxy, если никакая policy не требует body inspection.

Конфиг должен иметь:

`max_request_body_bytes` — абсолютный предел.

`max_inspect_body_bytes` — предел для parsing/logging.

Большие bodies могут proxy'ться без полного inspection, если policy это допускает.

Если policy требует token preflight, а body невозможно безопасно оценить, должна существовать определённая стратегия: reject, conservative reservation или usage-only. Поведение задаётся configuration/policy.

## 10. Header policy

Gateway должен копировать end-to-end headers и удалять hop-by-hop headers согласно HTTP semantics.

Необходимо заменить client Authorization на upstream credential.

Нельзя отправлять 9router gateway API key обратно клиенту ни в headers, ни в logs.

Полезно добавить внутренние response headers:

`X-Gateway-Request-ID`

Опционально, в debug mode:

`X-Gateway-Upstream-Latency-Ms`

`X-Gateway-TTFT-Ms`

Но debug headers должны отключаться configuration.

`Content-Length` нельзя слепо сохранять при любом response transformation. При SSE → JSON создаётся новый body и новый размер.

## 11. Upstream Transport

Предпочтительно использовать один долгоживущий `http.Client`/`http.Transport`, а не создавать client на каждый request.

Transport должен поддерживать connection pooling и keep-alive.

Основные configurable параметры:

`dial_timeout`

`tls_handshake_timeout`

`response_header_timeout`

`idle_conn_timeout`

`max_idle_conns`

`max_idle_conns_per_host`

`max_conns_per_host`, по умолчанию без искусственно малого значения.

Нельзя ставить `MaxConnsPerHost=1`, иначе получится скрытая сериализация requests.

Streaming request не должен иметь короткого общего `http.Client.Timeout`, потому что он ограничит длительность всей генерации. Deadline должен приходить из request context/policy.

## 12. Определение response mode

Response classifier должен опираться прежде всего на upstream headers.

Концептуально:

```go
type ResponseMode int

const (
    ResponseJSON ResponseMode = iota
    ResponseSSE
    ResponseOpaque
)
```

`Content-Type: text/event-stream` → SSE.

`application/json`, `application/*+json` → JSON.

Остальное → opaque passthrough.

Если upstream Content-Type отсутствует или явно ошибочен, можно реализовать ограниченный sniffing первых байтов, но это fallback. Не нужно превращать gateway в content detection engine.

Особенно важно: client `stream:false` + upstream SSE классифицируется как SSE.

## 13. Streaming passthrough

Это главный hot path.

Функция conceptually:

```go
type StreamObserver interface {
    Observe(chunk []byte)
    Finish(err error)
}
```

Transport читает upstream body небольшим reusable buffer и сразу пишет прочитанные байты downstream.

После каждого успешного write вызывается Flush, если ResponseWriter поддерживает `http.Flusher`.

Observer получает копию или transient view chunk'а для telemetry parser. Observer не имеет права блокировать transport надолго.

Если observer потенциально тяжёлый, используется отдельный bounded asynchronous channel. При переполнении telemetry может быть частично отброшена; transport блокировать нельзя.

Нужно внимательно решить lifetime buffer: нельзя отправить `[]byte` в goroutine и затем переиспользовать этот же buffer без копирования. Если asynchronous parsing используется, данные нужно копировать либо использовать pool ownership model.

Для первого MVP проще синхронный лёгкий SSE parser без SQLite операций.

## 14. SSE parser

SSE parser должен быть generic и корректным согласно event-stream framing.

Он должен понимать:

`event: ...`

`data: ...`

комментарии `: heartbeat`

пустую строку как конец event.

Несколько `data:` строк одного event должны объединяться корректно.

Нельзя предполагать, что один `Read()` соответствует одному SSE event.

Нельзя предполагать, что строка целиком находится в одном `Read()`.

Parser должен работать при:

`da` + следующий read `ta: {...}`

и при одном read, содержащем сразу пять events.

Максимальный размер одного SSE event должен иметь configurable safety limit.

Generic SSE parser возвращает:

```go
type SSEEvent struct {
    Event string
    Data  []byte
}
```

Он ничего не знает про OpenAI.

## 15. OpenAI Stream Observer

Поверх generic SSE parser работает OpenAI observer.

Он извлекает только необходимые metadata:

- response ID;
- model;
- choice indexes;
- content;
- role;
- finish_reason;
- tool_calls;
- usage;
- `[DONE]`.

В passthrough streaming observer не влияет на передачу данных.

Он обновляет RequestTrace и UsageAccumulator.

Если parser не смог разобрать один OpenAI chunk, это не должно обязательно рвать passthrough stream. Нужно сохранить parsing error в telemetry и продолжить proxy, если transport сам исправен. Transparent proxy важнее observability.

Исключение — режим, где parser необходим для policy enforcement в real time. Такой режим должен быть явным.

## 16. SSE → JSON accumulator

При client `stream:false` и upstream SSE нужен другой path. Здесь response нельзя передавать клиенту по мере получения, потому что клиент ожидает один JSON.

В этом режиме gateway читает весь SSE stream и собирает итоговый OpenAI-compatible response.

Предлагаемый интерфейс:

```go
type ChatCompletionAccumulator interface {
    Add(event SSEEvent) error
    Complete() (*ChatCompletionResponse, error)
}
```

Accumulator должен поддерживать несколько choices.

Для каждого choice хранится:

- role;
- content builder;
- finish reason;
- tool calls map/order.

Для tool calls:

- index;
- id;
- type;
- function name;
- `strings.Builder` для function arguments.

Usage берётся из последнего доступного usage object.

Response ID/model/created берутся из upstream chunks.

EOF считается допустимым завершением даже без `[DONE]`, если stream уже содержит достаточный корректный response. Именно это необходимо для 9router.

Если EOF пришёл до любого meaningful chunk, возвращается upstream/protocol error.

## 17. Streaming state machine

Для transparent passthrough транспортная state machine очень простая:

`INIT → HEADERS_RECEIVED → STREAMING → EOF/CANCEL/ERROR → CLOSED`

`finish_reason` не переводит transport в CLOSED.

`[DONE]` также не обязан самостоятельно закрывать upstream transport: после `[DONE]` gateway может продолжить чтение до EOF, но нельзя искусственно удерживать downstream после того, как upstream завершился.

Практический вариант для минимальной latency: передать `[DONE]`, продолжить чтение upstream до EOF, но если upstream после `[DONE]` зависает, downstream уже логически получил terminal marker. Нужно решить, стоит ли закрывать downstream сразу после `[DONE]`. Для OpenAI-compatible endpoint допустимо закрыть downstream после `[DONE]`, одновременно cancel upstream context, потому что `[DONE]` является явным protocol terminal event.

Для upstream без `[DONE]` нормальным концом является EOF.

Для aggregation state machine более содержательна:

`INIT`

`→ EVENTS`

`→ TERMINAL_SEEN` при `finish_reason != null`

`→ USAGE_SEEN` опционально

`→ DONE` при `[DONE]` или EOF

Наличие `TERMINAL_SEEN` не обязательно означает, что нужно остановить чтение, потому что usage может прийти отдельным последующим chunk.

## 18. Зависший upstream после terminal event

Это отдельный edge case.

Если upstream прислал `finish_reason`, но не прислал `[DONE]` и не закрыл connection, gateway не должен автоматически закрывать обычный transparent stream только на основании `finish_reason`, потому что могут существовать последующие usage events.

Однако нужен compatibility mechanism для известных broken upstream patterns.

Предлагаемый policy:

`terminal_grace_timeout`

После terminal event, если не приходит новых meaningful events, gateway может через короткий configurable grace period завершить aggregation или streaming compatibility mode.

По умолчанию для прозрачного passthrough эта функция выключена.

Для 9router она может быть включена только если реально понадобится. В текущем наблюдаемом поведении 9router после terminal event сам быстро закрывает stream, поэтому никакой grace timeout не требуется.

Главная ошибка Bifrost не должна быть повторена: нельзя использовать многосекундное ожидание после того, как upstream уже физически закончил body.

## 19. JSON passthrough

Для client non-stream + upstream JSON gateway по возможности не должен deserialize/serialize response полностью.

Можно использовать tee reader для bounded body capture/accounting parser, но raw JSON bytes должны быть переданы без изменения.

Если response небольшой и usage parsing нужен, допустимо прочитать body полностью, извлечь usage и отправить исходные bytes клиенту.

Главное — не marshal обратно из Go structs без необходимости, потому что это может менять unknown fields, number formatting и порядок полей.

Для OpenAI JSON responses parser должен использовать tolerant structures или selective JSON extraction. Неизвестные поля игнорируются для accounting, но сохраняются в raw response.

## 20. Auth interface

Предлагаемый интерфейс:

```go
type Authenticator interface {
    Authenticate(ctx context.Context, rawKey string) (*APIKey, error)
}
```

`APIKey` содержит ID и policy reference, но никогда не возвращает raw secret после создания.

Для lookup можно хранить prefix + keyed hash.

Например raw key:

`sk-gw-AbCd...`

В БД:

`key_prefix = sk-gw-AbCd`

`key_hash = HMAC-SHA256(server_pepper, raw_key)`

Prefix нужен для удобного определения записи/отображения, hash — для verification.

Server pepper хранится вне SQLite в environment/config secret.

При создании raw key показывается администратору один раз.

## 21. Policy model

EffectivePolicy должен быть immutable объектом на lifetime request.

Пример:

```go
type EffectivePolicy struct {
    AllowedModels     []Pattern
    DeniedModels      []Pattern
    RequestLimits     []RequestWindowLimit
    TokenLimits       []TokenWindowLimit
    MaxConcurrency    int
    Budget            *BudgetPolicy
    TokenMode         TokenAccountingMode
    Logging           LoggingPolicy
}
```

Pattern matching для моделей лучше на первом этапе ограничить простым exact + glob.

Не добавлять regex без необходимости.

Policy resolution выполняется один раз до upstream request.

## 22. Request rate limiter

Для произвольных временных окон проще всего использовать sliding window counters или bucketed counters.

При single-instance можно держать runtime state:

`map[keyID+limitID]*WindowCounter`

Для minute/hour/day допустим ring of time buckets.

Не обязательно хранить timestamp каждого request.

Например 60 RPM можно представить 60 one-second buckets.

Для hour/day можно использовать более крупные bucket widths.

Важно определить semantics на границе windows. Для MVP можно использовать fixed windows, если реализация существенно проще, но тогда это должно быть явно документировано.

Request limit consumption происходит до upstream call и обычно не возвращается при upstream error: запрос уже был сделан пользователем и потребил gateway capacity.

## 23. Concurrency limiter

Для каждого key с concurrency limit хранится semaphore.

Не создавать отдельную goroutine для каждого waiting request без bound.

Policy должна определять поведение при занятости всех slots:

предпочтительно **reject immediately with 429**, а не бесконечно ставить запросы в очередь.

Опционально позже можно добавить небольшой queue timeout.

Для agent clients immediate 429 обычно предсказуемее скрытого ожидания.

## 24. Token windows и reservation

Runtime token limiter должен учитывать два значения:

`committed usage`

`reserved usage`

Проверка нового request:

`committed_in_window + reserved_in_window + requested_reservation <= limit`

Если нет — request отклоняется.

После completion:

`reserved -= reserved_amount`

`committed += actual_amount`

После abort с известным partial usage:

`reserved -= reserved_amount`

`committed += known_partial_actual`

Если actual usage неизвестен, policy должна решить, списать reservation полностью или использовать estimate. Для защиты бюджета безопаснее консервативно считать reservation потраченным при ambiguous upstream failure после начала generation.

Reservations должны иметь request ID, чтобы после process crash можно было понять, какие persistent reservations устарели. Однако для MVP runtime reservations можно держать только в RAM, потому что после restart все upstream connections всё равно потеряны.

## 25. Tokenizer interface

```go
type TokenEstimator interface {
    EstimateRequest(ctx context.Context, model string, rawBody []byte) (TokenEstimate, error)
}
```

```go
type TokenEstimate struct {
    InputTokens        int64
    MaxOutputTokens    int64
    Confidence         EstimateConfidence
}
```

Implementation chain:

1. exact tokenizer, если зарегистрирован для модели;
2. generic estimator;
3. conservative fallback.

Нельзя позволять tokenizer error автоматически ломать request, если policy не требует strict token enforcement.

Для `usage_only` estimator вообще не вызывается.

## 26. Generic token estimate

Для MVP generic estimator может использовать heuristic, но нужно честно считать его estimate.

Лучше слегка переоценивать usage, чем недооценивать.

Estimator должен учитывать не только `messages[].content`, но и system content, tools/functions schemas и другие текстовые части request.

Не нужно пытаться сделать heuristic tokenizer универсальным для image/audio input.

Multimodal request может иметь confidence `unknown` и использовать configurable fixed reservation.

## 27. Usage model

Общий internal type:

```go
type Usage struct {
    InputTokens     int64
    OutputTokens    int64
    TotalTokens     int64
    CachedTokens    int64
    ReasoningTokens int64
}
```

Поля, которых upstream не дал, остаются 0/unknown в зависимости от необходимости. Возможно, для точности стоит использовать nullable fields, но enforcement требует простых числовых значений.

Нужно отдельно сохранить raw upstream usage JSON в request telemetry при debug mode, чтобы новые provider-specific usage поля не терялись для диагностики.

## 28. Pricing

Денежные значения нельзя хранить в `float64`.

Использовать integer micro-units или decimal representation.

Например USD micros:

`1 USD = 1_000_000 micros`.

Pricing:

`input_price_per_1m_tokens_micros`

`output_price_per_1m_tokens_micros`

Cost:

`tokens * price / 1_000_000`

Нужно учитывать overflow, хотя реальные значения малы.

Pricing rule выбирается по model exact/glob match.

Если цены нет, request не должен падать, если budget enforcement не требует известной стоимости. Cost status устанавливается `unknown`.

Если budget strict и стоимость модели неизвестна, policy может запретить request.

## 29. Budget reservation

Budget reservation строится на token reservation и pricing.

Estimate cost определяется как:

estimated input + potential output.

Если модель дорогая и max output неизвестен, используется configured default max output reservation.

Budget state:

`spent_micros`

`reserved_micros`

`limit_micros`

Request допускается если:

`spent + reserved + newReservation <= limit`.

После completion reservation заменяется actual cost.

Периоды бюджета: total, hour/day/month можно реализовать общей window abstraction, но month требует календарного периода, а не `30 * 24h`.

Не следует пытаться сделать generic cron-like budget periods в MVP.

## 30. SQLite schema — общие правила

SQLite работает в WAL mode.

Включить foreign keys.

Migrations должны быть встроены в binary и применяться при startup до readiness.

Не использовать auto-schema из ORM как единственный механизм migrations.

Предпочтительно писать явные SQL migrations.

SQLite connection settings должны учитывать single-process concurrent reads/writes.

Большинство runtime limiter операций не должны выполнять SQL.

## 31. Таблица api_keys

Предлагаемая схема:

`id INTEGER PRIMARY KEY`

`name TEXT NOT NULL`

`key_prefix TEXT NOT NULL`

`key_hash BLOB NOT NULL UNIQUE`

`enabled INTEGER NOT NULL DEFAULT 1`

`expires_at INTEGER NULL`

`created_at INTEGER NOT NULL`

`updated_at INTEGER NOT NULL`

`last_used_at INTEGER NULL`

`policy_json TEXT NOT NULL`

На первом этапе policy допустимо хранить JSON целиком. Это сильно упрощает развитие структуры limits без десятка relational tables.

Если позже появится сложный Web UI/reporting по policy, schema может нормализоваться.

Raw key в БД не хранится.

## 32. Таблица requests

`id TEXT PRIMARY KEY`

`api_key_id INTEGER NULL`

`started_at INTEGER NOT NULL`

`finished_at INTEGER NULL`

`method TEXT NOT NULL`

`path TEXT NOT NULL`

`model TEXT NULL`

`client_stream INTEGER NULL`

`upstream_mode TEXT NULL`

`status_code INTEGER NULL`

`termination_reason TEXT NULL`

`request_bytes INTEGER NULL`

`response_bytes INTEGER NULL`

`input_tokens INTEGER NULL`

`output_tokens INTEGER NULL`

`total_tokens INTEGER NULL`

`cost_micros INTEGER NULL`

`cost_known INTEGER NOT NULL DEFAULT 0`

`ttft_ms INTEGER NULL`

`upstream_headers_ms INTEGER NULL`

`last_content_ms INTEGER NULL`

`stream_close_delay_ms INTEGER NULL`

`total_duration_ms INTEGER NULL`

`error_code TEXT NULL`

`error_message TEXT NULL`

`created_at INTEGER NOT NULL`

Foreign key на api_keys должен быть `ON DELETE SET NULL`, чтобы удаление key не уничтожало историю.

Indexes как минимум:

`started_at`

`api_key_id, started_at`

`model, started_at`

`status_code, started_at`

Не индексировать всё подряд.

## 33. Таблица request_bodies

Body inspection лучше вынести из основной requests table.

`request_id TEXT PRIMARY KEY`

`client_body BLOB NULL`

`upstream_body BLOB NULL`

`response_body BLOB NULL`

`client_body_truncated INTEGER NOT NULL DEFAULT 0`

`upstream_body_truncated INTEGER NOT NULL DEFAULT 0`

`response_body_truncated INTEGER NOT NULL DEFAULT 0`

`client_body_original_bytes INTEGER NULL`

`response_body_original_bytes INTEGER NULL`

Можно хранить TEXT, если гарантирован UTF-8 JSON, но BLOB универсальнее.

Retention body должен быть отдельно configurable и потенциально короче metadata retention.

В будущем можно gzip-compress большие body, но не в первом MVP.

## 34. Таблица usage_buckets

Чтобы не сканировать миллионы requests для dashboard/budgets:

`id INTEGER PRIMARY KEY`

`api_key_id INTEGER NOT NULL`

`bucket_start INTEGER NOT NULL`

`bucket_seconds INTEGER NOT NULL`

`requests INTEGER NOT NULL`

`input_tokens INTEGER NOT NULL`

`output_tokens INTEGER NOT NULL`

`total_tokens INTEGER NOT NULL`

`cost_micros INTEGER NOT NULL`

Unique:

`api_key_id, bucket_start, bucket_seconds`

Для MVP можно использовать только hourly/day aggregates и реальные limiter windows держать в RAM.

Не нужно превращать эту таблицу в source of truth для sub-minute rate limiter.

## 35. Таблица pricing_rules

Можно хранить pricing в YAML, что проще для первого релиза. Если нужен UI — переносим в SQLite.

При SQLite:

`id INTEGER PRIMARY KEY`

`model_pattern TEXT NOT NULL`

`input_per_million_micros INTEGER NOT NULL`

`output_per_million_micros INTEGER NOT NULL`

`priority INTEGER NOT NULL`

`enabled INTEGER NOT NULL`

`created_at`

`updated_at`

Если pricing остаётся config-only в MVP, таблицу не создавать заранее.

## 36. Таблица settings

Не нужна, если системная конфигурация хранится в YAML/env.

Не следует складывать все конфиги одновременно и в YAML, и в SQLite, создавая два source of truth.

Runtime/admin managed сущности — SQLite.

Deployment settings/secrets — YAML/env.

## 37. Telemetry writer

После request completion формируется immutable `RequestRecord`, который отправляется в bounded channel.

Одна или небольшое число writer goroutines выполняют batched SQLite writes.

Если telemetry queue заполнена, стратегия должна быть определена. Для обычной request history допустимо drop с metric `telemetry_dropped_total`.

Но usage, влияющий на budget, нельзя потерять. Поэтому enforcement accounting state обновляется синхронно в RAM до отправки telemetry record. Persistence aggregates можно выполнять отдельно.

Не смешивать критический limiter state и необязательную detailed telemetry.

## 38. Request body capture

Body capture реализуется через bounded recorder.

Он сохраняет только первые `N` bytes, но считает полный original size.

Sensitive headers никогда не попадают в body capture.

В JSON body потенциально могут находиться secrets пользователей. Gateway не должен пытаться автоматически redaction'ить произвольный prompt текст — это невозможно сделать надёжно. Поэтому body logging должен быть opt-in и это нужно явно указать в документации.

При включённом body logging администратор осознанно принимает, что prompts могут содержать конфиденциальные данные.

## 39. Error model

Внутренняя ошибка:

```go
type GatewayError struct {
    HTTPStatus int
    Code       string
    Message    string
    RetryAfter *time.Duration
    Cause      error
}
```

Cause используется только logs, не отдаётся клиенту напрямую.

Client response:

```json
{
  "error": {
    "message": "...",
    "type": "...",
    "code": "..."
  }
}
```

Минимальные gateway codes:

`invalid_api_key`

`key_disabled`

`key_expired`

`model_not_allowed`

`request_rate_limit_exceeded`

`token_limit_exceeded`

`concurrency_limit_exceeded`

`budget_exceeded`

`request_too_large`

`upstream_connection_error`

`upstream_timeout`

`stream_protocol_error`

`gateway_internal_error`

Если upstream вернул валидный HTTP error response, по умолчанию он проксируется как есть.

## 40. Request termination reasons

Telemetry должна различать:

`completed`

`client_cancelled`

`gateway_rate_limited`

`gateway_budget_rejected`

`gateway_token_rejected`

`gateway_hard_limit`

`upstream_error`

`upstream_timeout`

`upstream_eof`

`protocol_error`

`internal_error`

`upstream_eof` при нормальном SSE без `[DONE]` не является ошибкой; возможно лучше normal termination оставить `completed` с отдельным `stream_terminal=eof`.

## 41. Health endpoints

`GET /health` проверяет только, что process жив и HTTP server отвечает.

`GET /ready` проверяет:

- config loaded;
- SQLite available;
- migrations completed;
- required secrets configured;
- core services initialized.

Не выполнять upstream LLM request.

Можно опционально иметь `/admin/upstream/check`, который делает лёгкую проверку `/v1/models`, но это не readiness.

## 42. Admin API

Отдельный route prefix:

`/admin/v1/...`

Authentication — master admin token, отличный от gateway API keys.

Минимум:

`POST /admin/v1/keys`

`GET /admin/v1/keys`

`GET /admin/v1/keys/{id}`

`PATCH /admin/v1/keys/{id}`

`DELETE /admin/v1/keys/{id}` либо revoke.

`GET /admin/v1/requests`

`GET /admin/v1/requests/{id}`

`GET /admin/v1/usage`

Создание key возвращает raw key только один раз.

Admin API не должен слушать отдельный public port по умолчанию. Можно иметь настройку bind/admin exposure позднее.

## 43. CLI

CLI является thin client admin API.

Примеры:

`gwctl key create opencode`

`gwctl key list`

`gwctl key disable <id>`

`gwctl key set-limit <id> --rpm 60`

`gwctl request list --key opencode`

`gwctl request show <request-id>`

В development mode CLI может поддерживать local admin socket или localhost endpoint.

Не давать CLI прямой доступ к SQLite после появления admin API, иначе появятся две реализации business logic.

## 44. Configuration file

Ориентировочная структура:

`server`

`upstream`

`storage`

`timeouts`

`logging`

`tokenizer`

`defaults`

`pricing`

Секреты через environment variables.

Конфиг должен проходить validation до запуска listener.

Unknown YAML fields желательно считать ошибкой или warning, чтобы опечатки не игнорировались тихо.

Изменение configuration через hot reload не является MVP. Restart одного лёгкого Go container достаточно.

## 45. Startup lifecycle

1. Parse command.
2. Load configuration.
3. Resolve environment secrets.
4. Validate config.
5. Open SQLite.
6. Apply migrations.
7. Load API key policies/index into memory.
8. Initialize limiter state.
9. Initialize HTTP transport.
10. Initialize telemetry writer.
11. Start HTTP server.
12. Mark ready.

Shutdown:

1. Stop accepting new requests.
2. Give active requests configurable grace period.
3. Cancel remaining requests.
4. Flush critical accounting.
5. Flush telemetry queue within bounded timeout.
6. Close SQLite.
7. Exit.

## 46. API key cache

Не нужно делать SQL query на каждый request.

При startup загрузить enabled key hashes/prefix index в memory.

Admin changes обновляют SQLite и затем atomic in-memory snapshot/cache.

Authentication hot path должен быть lock-light.

Например immutable map snapshot через `atomic.Value`.

Last-used timestamp не нужно синхронно писать в SQLite на каждый request; обновлять batched.

## 47. Policy cache

Policy хранится вместе с API key snapshot.

EffectivePolicy может быть предварительно скомпилирован:

- parsed duration;
- model glob matcher;
- budget policy;
- limit identifiers.

Не parse JSON/duration/glob на каждый request.

## 48. Runtime limiter registry

Runtime state keyed by stable API key ID и limit ID.

Удаление API key должно удалить runtime state либо позволить GC/cleanup.

Нужно иметь periodic cleanup для expired window buckets и disabled/deleted key state.

Не создавать неограниченно растущий map на каждый уникальный model/request path, если model-specific limits не используются.

## 49. Forced termination во время streaming

Hard token enforcement в реальном времени возможен только если gateway может считать generated tokens достаточно быстро.

Это сложнее, чем post-response usage.

Для MVP рекомендуется:

- strict pre-request reservation;
- post-request reconciliation;
- не рвать stream посередине по estimated output tokens.

Hard mid-stream termination оставить optional later feature.

Иначе легко получить неправильную токенизацию, повреждённые tool calls и плохой UX.

Budget также предпочтительно enforce до request через reservation, а не обрывать generation за несколько центов до конца.

Administrative kill — отдельное исключение.

## 50. `/v1/models`

В первом MVP `/v1/models` можно прозрачно proxy'ть.

Если у key есть model allow/deny, полезно фильтровать response, но это уже модификация payload.

Можно выбрать более простой semantics: запрет применяется только при generation request, а `/models` показывает весь upstream catalog.

Это предпочтительно для MVP, потому что меньше protocol transformations.

Позже UI/client experience можно улучшить фильтрацией.

## 51. `/v1/responses`

Не следует сразу писать сложный Responses API parser, если OpenCode его не использует.

Endpoint generic passthrough должен работать с первого дня.

Specialized usage/stream parser для Responses API добавляется отдельным модулем, когда появляется реальная необходимость.

Архитектура streaming должна позволять зарегистрировать protocol observer по path.

Например:

`/v1/chat/completions → ChatCompletionObserver`

`/v1/responses → ResponsesObserver`

unknown path → NoopObserver.

## 52. Protocol observer registry

Концептуально:

```go
type ProtocolHandler interface {
    InspectRequest(body []byte) RequestMetadata
    NewStreamObserver() StreamObserver
    ParseJSONUsage(body []byte) UsageResult
    AggregateSSE(events <-chan SSEEvent) ([]byte, UsageResult, error)
}
```

Registry выбирает handler по method/path.

Важно не создавать giant interface, где generic proxy обязан реализовать всё. На практике interfaces лучше разделить на маленькие capability interfaces:

`RequestInspector`

`StreamUsageObserver`

`JSONUsageParser`

`SSEAggregator`.

Generic endpoint просто не имеет capabilities.

## 53. Metrics

Prometheus можно добавить после core, но internal metric names стоит определить заранее.

Минимальные:

`gateway_requests_total`

`gateway_requests_active`

`gateway_request_duration_seconds`

`gateway_ttft_seconds`

`gateway_stream_close_delay_seconds`

`gateway_input_tokens_total`

`gateway_output_tokens_total`

`gateway_cost_micros_total`

`gateway_rejected_requests_total{reason}`

`gateway_upstream_errors_total`

`gateway_client_cancellations_total`

`gateway_telemetry_dropped_total`

Не использовать API key name как Prometheus label по умолчанию, если keys много; это создаёт high cardinality. Для self-hosted small deployment можно опционально включить.

Request ID никогда не является metrics label.

## 54. Logging

Structured JSON logs в production.

Human-readable mode для development.

Каждый request log должен содержать request ID.

Не логировать Authorization.

Основные события:

`request_started`

`request_rejected`

`upstream_started`

`upstream_headers`

`stream_started`

`request_completed`

`request_cancelled`

`upstream_error`

Не логировать каждый token/chunk по умолчанию.

Debug chunk logging можно иметь только development option.

## 55. Performance invariants

Нужно считать эти требования архитектурными invariants.

Transparent SSE path не выполняет SQLite write перед Flush.

Transparent SSE path не выполняет tokenizer на каждом chunk.

Transparent SSE path не ждёт telemetry writer.

Один request не блокирует другой request без configured limiter.

EOF upstream немедленно освобождает transport.

Client cancellation распространяется upstream.

Не создаётся новый HTTP client на каждый request.

Не выполняется JSON reserialization при обычном passthrough.

## 56. Benchmark baseline

До добавления limiter необходимо сохранить benchmark прямого 9router:

- TTFT;
- total generation;
- finish-to-close;
- parallel requests.

После каждого крупного этапа сравнивать:

`direct 9router`

против

`gateway → 9router`.

Цель — видеть не только requests/sec, а именно latency coding-agent workflow.

Особый automated benchmark:

mock upstream отдаёт terminal data и EOF за X ms.

Gateway downstream EOF должен появиться практически сразу, например с overhead < 50 ms в CI environment.

Не задавать слишком жёсткий 1–2 ms threshold, который будет flaky на GitHub Actions.

## 57. Integration test infrastructure

Mock upstream должен быть полноценным HTTP server, а не только mocked interface.

Это необходимо, чтобы реально тестировать:

- chunk boundaries;
- Flush;
- EOF;
- cancellation;
- content-type;
- slow responses;
- hanging connections.

Каждый scenario запускается через настоящий gateway HTTP server.

Для timing regression использовать generous threshold: например upstream EOF → downstream EOF < 250 ms в CI. Локально значение должно быть намного меньше.

## 58. Ключевые integration tests

`TestTransparentSSEImmediateEOF`

Upstream: chunk, finish_reason, EOF. Проверить отсутствие `[DONE]` requirement и немедленный downstream EOF.

`TestTransparentSSEWithDone`

Upstream: chunks, `[DONE]`, EOF. Проверить byte-equivalent payload.

`TestNonStreamRequestWithSSEUpstream`

Client: `stream:false`. Upstream: SSE. Проверить HTTP 200 JSON и собранный content.

`TestToolCallAggregation`

Arguments разбиты между несколькими events.

`TestChunkBoundaryIndependence`

SSE `data:` разбито произвольными TCP/write boundaries.

`TestConcurrentRequests`

Два upstream handlers заблокированы barrier'ом; убедиться, что оба начали одновременно.

`TestConcurrencyLimit`

Limit=1; второй request получает defined 429 или queue behavior.

`TestClientCancellation`

Client отменяет context; mock upstream наблюдает cancellation.

`TestUnknownEndpointPassthrough`

Binary body/unknown content-type проходят без изменения.

`TestUpstreamErrorPassthrough`

9router-like JSON error сохраняется.

`TestTelemetryDoesNotBlockStream`

Искусственно медленный telemetry writer не влияет на SSE delivery.

## 59. Unit tests limiter

Request windows проверяются с injectable Clock. Нельзя писать unit tests, которые реально ждут минуту.

Интерфейс:

```go
type Clock interface {
    Now() time.Time
}
```

Production `RealClock`, tests `FakeClock`.

Та же идея нужна для budget periods и token windows.

## 60. Security baseline

Upstream API key никогда не принимается от client как source of truth.

Client не может переопределить upstream base URL через headers/query.

Запрещён arbitrary proxy target, иначе gateway превратится в SSRF/open proxy.

Все `/v1/*` идут только на configured 9router base URL.

Request path нужно безопасно join'ить с base URL, не позволяя `//evil.com` и подобные tricks изменить host.

Admin API имеет отдельную auth policy.

SQLite file permissions должны быть ограничены.

Request bodies могут содержать secrets и считаются sensitive data.

## 61. Docker deployment

Один основной container.

Volume:

`./data:/data`

Config:

`./config.yaml:/etc/gateway/config.yaml:ro`

SQLite:

`/data/gateway.db`

Никаких обязательных companion services.

Healthcheck вызывает `/health`.

Container должен корректно обрабатывать SIGTERM.

По возможности non-root user внутри image.

Multi-stage Docker build.

## 62. Возможность заимствования Bifrost

При реализации limiter/tokenizer/accounting можно изучать Bifrost и адаптировать хорошо изолированные части, если это сокращает количество ошибок.

Нельзя переносить его transport architecture только ради reuse.

Перед копированием конкретного файла нужно проверить:

- действительно ли код нужен;
- не тянет ли он половину Bifrost dependencies;
- можно ли выделить алгоритм намного проще;
- какие license/NOTICE требования применяются.

Если код copied/adapted, комментарий в source не обязан стоять на каждой функции, но repository должен корректно сохранять license/NOTICE attribution.

Имеет смысл вести `THIRD_PARTY_NOTICES.md`.

## 63. Порядок реализации пакетов

### Milestone 0 — repository skeleton

Создать Go module, basic config, logging, Dockerfile, тестовую инфраструктуру. Никакого limiter.

### Milestone 1 — raw proxy

Реализовать transport и generic `/v1/*` proxy. Авторизация пока может быть одним static development key.

Критерий: OpenCode работает через gateway так же быстро, как напрямую.

### Milestone 2 — correct streaming

Реализовать explicit Flush, cancellation, response mode classification и mock SSE tests.

Критерий: никаких дополнительных секунд после EOF.

Это самый важный milestone проекта.

### Milestone 3 — OpenAI request inspection

Извлекать model, stream, max_tokens. Добавить JSON usage parser и stream observer.

Критерий: accounting metadata появляется, transport benchmarks не ухудшаются существенно.

### Milestone 4 — SSE aggregation

Реализовать `stream:false → upstream SSE → JSON`.

Критерий: сценарий, на котором Bifrost выдавал `failed to unmarshal response`, проходит integration test и работает с реальным 9router.

### Milestone 5 — API key auth

SQLite migrations, api_keys, hash verification, in-memory cache.

Критерий: keys можно создать/revoke; invalid key никогда не достигает upstream.

### Milestone 6 — concurrency и request limits

Request windows и per-key semaphore.

Критерий: parallel requests разрешены по умолчанию, configured limits работают детерминированно.

### Milestone 7 — token estimation и reservations

Tokenizer interface, generic estimator, token windows, RequestLease.

Критерий: два concurrent reservations не могут вместе превысить strict token limit.

### Milestone 8 — budget

Pricing, cost reservation, reconciliation.

Критерий: concurrent requests не overspend configured budget.

### Milestone 9 — observability persistence

requests table, body capture, async telemetry.

Критерий: можно открыть request и увидеть metadata/body, при этом streaming path не зависит от SQLite latency.

### Milestone 10 — Admin API/CLI

Key management, policy editing, request inspection.

### Milestone 11 — metrics

Prometheus и benchmark suite.

### Milestone 12 — Web UI

Только после того, как предыдущие milestones стабильны.

## 64. Что OpenCode не должен делать преждевременно

Не начинать с Web UI.

Не добавлять Redis.

Не добавлять PostgreSQL.

Не добавлять Anthropic API.

Не добавлять provider abstraction для Anthropic/OpenAI/Gemini.

Не писать собственный 9router.

Не добавлять load balancing.

Не писать generalized plugin system.

Не добавлять retries после начала streaming.

Не делать giant `LLMProvider` interface.

Не переделывать raw passthrough body в внутреннюю canonical schema.

Не оптимизировать до появления benchmark.

Не добавлять middleware framework ради middleware framework.

## 65. Минимальные interfaces первой реализации

На первом этапе достаточно следующих концепций:

`Authenticator`

`PolicyProvider`

`Limiter`

`RequestLease`

`UpstreamTransport`

`ResponseClassifier`

`SSEParser`

`StreamObserver`

`UsageParser`

`TokenEstimator`

`AccountingService`

`RequestRepository`

И даже они не обязаны появиться все сразу. Не нужно заранее создавать пустые interfaces, если пока существует только одна implementation и нет реальной границы для тестирования.

Interface вводится там, где есть architectural boundary или необходимость подмены в tests.

## 66. Source of truth

API keys/policies: SQLite.

Runtime active reservations: memory.

Request/time-window live counters: memory.

Historical usage: SQLite aggregates/requests.

Deployment config: YAML/env.

Upstream model/provider routing: 9router.

Actual token usage: upstream `usage`, если доступен.

Estimated token usage: tokenizer/estimator.

Actual response bytes/lifetime: upstream HTTP response, а не protocol assumptions.

## 67. Основной критерий архитектурных решений

Любое изменение должно проходить три вопроса.

Первый: может ли эта функция задержать первый или последний token? Если да, её нельзя помещать синхронно в streaming hot path без веской причины.

Второй: пытается ли gateway решить задачу, которую уже решает 9router? Если да, функциональность скорее всего не нужна.

Третий: можно ли сохранить request/response без изменения? Если да, следует сохранить его без изменения.

Проект должен оставаться прежде всего быстрым и предсказуемым шлагбаумом перед 9router: он знает, кто имеет право проехать, сколько может потратить и сколько уже потратил, но не пытается управлять тем, что находится внутри машины.
