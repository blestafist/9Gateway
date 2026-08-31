# PLAN.md — Lightweight LLM Gateway / Limiter for 9router

## 1. Назначение проекта

Нужно разработать лёгкий self-hosted LLM gateway, который устанавливается между клиентами и 9router и выполняет только инфраструктурные функции: авторизация, ограничения, учёт токенов и расходов, наблюдаемость и корректное проксирование HTTP/SSE. Gateway не должен становиться ещё одним LLM-router, преобразователем провайдеров или универсальным AI framework. Всю маршрутизацию между Claude/OpenAI/Kiro/другими провайдерами, преобразование форматов и provider-specific логику уже выполняет 9router.

Основная схема:

`Client → Gateway → 9router → Provider`

Примеры клиентов: OpenCode, Telegram-боты, OpenAI SDK, curl, собственные приложения.

Главный архитектурный принцип проекта: **transparent by default, smart only when required**. Если запрос или ответ можно передать без изменения — он должен быть передан без изменения. Gateway вмешивается в payload только тогда, когда это необходимо для лимитов, учёта или совместимости streaming/non-streaming.

Проект должен быть существенно легче Bifrost/LiteLLM: один небольшой сервис, желательно один Go binary, SQLite вместо обязательного PostgreSQL/Redis, минимальное потребление RAM и отсутствие тяжёлой внутренней инфраструктуры.

## 2. Почему появился этот проект

Изначально для этой задачи использовался Bifrost как limiter перед 9router. Функционально Bifrost подходит хорошо: API keys, budgets, RPM/TPM, token accounting, tokenizer, статистика, возможность видеть тело запросов и другие governance-функции. Проблемой оказалась транспортная часть и обработка OpenAI-compatible streaming.

Первый обнаруженный баг возник при non-stream запросах. Клиент отправлял:

`"stream": false`

через Bifrost в 9router. Некоторые upstream-провайдеры 9router не поддерживают настоящий non-streaming mode, поэтому 9router внутри принудительно использует streaming. В результате Bifrost ожидал обычный JSON, но получал SSE body вида:

`data: {"id":"chatcmpl-..." ...}`

После этого Bifrost пытался выполнить обычный JSON unmarshal и возвращал HTTP 500:

`failed to unmarshal response from provider API`

с ошибкой:

`Syntax error at index 1: invalid char`

То есть gateway определял формат ответа исходя из значения `stream` во входящем request, а не из фактического Content-Type и содержимого upstream response. Это неправильное поведение для transparent gateway.

При `"stream": true` тот же маршрут работал, что подтвердило, что проблема находилась именно в non-stream response parser Bifrost.

Вторая, более неприятная проблема была обнаружена при использовании OpenCode. Субъективно после установки Bifrost OpenCode стал заканчивать генерации заметно позже, хотя текст ответа уже был полностью получен. Это было измерено timestamp-тестом.

Через Bifrost последний terminal chunk:

`"finish_reason":"stop"`

приходил примерно в `23:47:34.856`, а:

`data: [DONE]`

только в `23:47:40.715`.

Таким образом Bifrost добавлял около **5.8–6 секунд end-of-stream latency уже после фактического завершения генерации**. Ранее между этими событиями также наблюдались SSE heartbeat-сообщения `: heartbeat`, однако даже после их исчезновения шестисекундная задержка осталась.

Прямой запрос в 9router показал другую картину: `finish_reason:"stop"` приходил, после чего upstream stream завершался практически сразу. Следовательно, задержка создавалась не 9router, не Docker network и не Cloudflare, а самим gateway.

Была также отдельно проверена обработка параллельных запросов. Два запроса через Bifrost стартовали практически одновременно, первые данные получили через приблизительно одинаковое время и закончили выполнение одновременно. Поэтому проблема OpenCode не была связана с отсутствием concurrency в Bifrost. Причиной был именно lifecycle streaming response.

Проверялись разные версии Bifrost, включая более старые, однако проблема с завершением stream продолжала воспроизводиться. Поэтому дальнейший перебор версий признан бессмысленным.

Именно эти проблемы формируют основные технические требования нового проекта: gateway не должен определять формат ответа по предположениям, не должен удерживать завершённый stream, не должен ждать искусственный timeout после terminal response и не должен мешать нормальной параллельной работе клиентов.

## 3. Основная зона ответственности

Gateway является **policy enforcement proxy**, а не LLM router.

Он должен:

- принимать OpenAI-compatible HTTP запросы;
- проверять API key;
- применять ограничения;
- проксировать запрос в 9router;
- проксировать ответ обратно клиенту;
- считать токены и стоимость;
- сохранять статистику;
- позволять инспектировать request/response;
- немедленно отменять upstream request при disconnect клиента;
- корректно работать с streaming и non-streaming независимо от того, что фактически вернул upstream.

Gateway не должен:

- самостоятельно выбирать конечного AI-провайдера;
- реализовывать Claude/OpenAI/Gemini provider adapters;
- переводить Anthropic API в OpenAI API;
- менять tool calls;
- менять prompts;
- нормализовывать provider-specific payload;
- заниматься prompt caching;
- модифицировать reasoning content;
- быть agent framework;
- автоматически повторять уже начавшийся streaming request после передачи данных клиенту.

9router уже выполняет provider routing и protocol translation. Дублировать эту функциональность нельзя.

## 4. Поддерживаемый API

Основной публичный интерфейс — OpenAI-compatible `/v1/*`.

На первом этапе gateway должен полноценно понимать:

- `/v1/chat/completions`
- `/v1/responses`, если он используется клиентами
- `/v1/models`

Все остальные `/v1/*` endpoints должны поддерживаться в generic passthrough mode. Неизвестный endpoint не должен приводить к ошибке gateway только потому, что текущая версия кода ничего о нём не знает.

Generic proxy обязан сохранять:

- HTTP method;
- path;
- query parameters;
- request body;
- необходимые request headers;
- response status;
- response headers;
- response body.

Нельзя проектировать систему так, чтобы добавление нового endpoint в 9router требовало обновления gateway.

## 5. Streaming — критически важная часть

Streaming transport должен быть максимально простым и отделённым от governance/accounting.

Для обычного `stream=true → SSE upstream` gateway не должен реконструировать chunks. Предпочтительная реализация — прямое чтение upstream body и немедленная запись полученных данных клиенту с Flush после каждого доступного фрагмента.

Gateway не должен ждать `[DONE]`, если upstream корректно завершил HTTP response через EOF. Upstream EOF должен немедленно приводить к downstream EOF.

Gateway не должен искусственно продолжать stream после получения фактического конца upstream body.

Gateway не должен добавлять heartbeat без явной необходимости.

Gateway не должен использовать idle timeout как механизм нормального завершения уже законченной генерации.

Protocol parsing при transparent streaming нужен только параллельно для telemetry/accounting и не должен управлять lifetime TCP/HTTP stream, если для этого нет веской причины.

Основной принцип:

`read upstream → write downstream → flush → repeat → upstream EOF → downstream EOF`

Нормальный proxy path не должен зависеть от значения `finish_reason`.

## 6. Несовпадение stream режима клиента и upstream

Необходимо поддерживать реальное поведение 9router, при котором клиент может запросить non-streaming response, но upstream фактически возвращает SSE.

Формат response должен определяться по фактическому response, прежде всего по `Content-Type`, а не только по полю `stream` входящего request.

Необходима следующая матрица поведения.

Client `stream=true`, upstream SSE: прозрачный streaming passthrough без буферизации.

Client `stream=false`, upstream JSON: обычный JSON passthrough.

Client `stream=false`, upstream SSE: gateway должен полностью прочитать SSE, собрать результат и вернуть клиенту один стандартный OpenAI-compatible JSON response.

Client `stream=true`, upstream JSON: не является обязательной функцией первого MVP. В дальнейшем можно синтезировать один или несколько SSE chunks, однако эту функцию нельзя добавлять ценой усложнения основного transport layer.

При SSE → JSON aggregation обязательно корректно поддерживать:

- content deltas;
- role;
- finish_reason;
- usage;
- tool_calls;
- function arguments, которые могут приходить частями;
- несколько choices, если upstream их вернул;
- отсутствие `[DONE]`;
- EOF непосредственно после terminal chunk.

Tool call arguments нельзя JSON-decode по каждому delta chunk. Их необходимо сначала конкатенировать, поскольку JSON может быть разбит между несколькими SSE событиями.

## 7. Авторизация

Gateway должен выдавать собственные API keys вида, например:

`sk-gw-...`

В persistent storage необходимо хранить не полный key, а безопасный hash или keyed hash.

Для каждого API key должны задаваться:

- display name;
- enabled/disabled;
- created_at;
- optional expiration;
- model allow list;
- model deny list;
- RPM/request limits;
- token limits;
- concurrency;
- budget;
- logging policy.

Один upstream key 9router может использоваться несколькими gateway keys.

При невалидном или отключённом ключе запрос не должен доходить до 9router.

Ошибки желательно возвращать в OpenAI-compatible формате.

## 8. Request rate limits

Система должна поддерживать не только жёстко заданный RPM, а generic request limits по time window.

Например:

`60 requests / 1 minute`

`1000 requests / 1 hour`

`10000 requests / 24 hours`

RPM является частным случаем generic request window.

Для MVP допустима in-memory реализация token bucket или sliding window.

Ответ при превышении:

HTTP 429 и `Retry-After`, если его можно корректно вычислить.

Лимиты должны поддерживаться как минимум per API key. В дальнейшем можно добавить global и model-specific policies.

## 9. Token limits

Необходимы ограничения токенов на временной интервал:

- TPM;
- tokens/hour;
- tokens/day;
- произвольный `N tokens / duration`.

Например:

`100000 tokens / 1m`

`1000000 tokens / 1h`

`10000000 tokens / 24h`

Нужно учитывать как input, так и output tokens.

Token accounting должен по возможности использовать upstream `usage` как source of truth.

Для pre-request enforcement необходима предварительная оценка токенов. Нельзя пропускать несколько параллельных больших запросов только потому, что каждый из них по отдельности проверяет текущий уже списанный usage.

Поэтому должна использоваться схема **reservation → reconciliation**.

Перед отправкой запроса резервируется приблизительное количество токенов. После получения реального `usage` reservation заменяется фактическим usage, а разница возвращается в доступный лимит.

Пример:

estimated prompt: 8000

requested max output: 16000

reserved: 24000

actual usage: 9200

refund: 14800

Если невозможно точно определить output reservation, должна использоваться консервативная конфигурируемая стратегия.

## 10. Tokenizer

Tokenizer является важной функцией, которую стоит сохранить из концепции Bifrost.

Требуется отдельный tokenizer/accounting interface, чтобы core limiter не был связан с конкретной реализацией tokenizer.

Желательны режимы:

`usage_only` — предварительная токенизация не выполняется, используются только реальные данные upstream. Самый лёгкий, но самый слабый enforcement при concurrency.

`estimate` — используется приблизительная локальная оценка prompt tokens. Это должен быть разумный default для первого MVP.

`exact` — используется model-specific tokenizer, если он доступен.

Не нужно пытаться сразу реализовать идеальные tokenizer'ы для всех моделей. Архитектура должна позволять добавлять их постепенно.

Допустимо изучить и адаптировать tokenizer/accounting код Bifrost, если это позволяет его лицензия и сохраняются необходимые notices. При заимствовании исходного кода нужно явно фиксировать происхождение и изменения.

## 11. Concurrency limits

Обязателен лимит одновременно выполняющихся запросов.

Пример:

`max_concurrent_requests: 4`

Ограничение применяется per API key.

В дальнейшем можно поддерживать:

- global concurrency;
- per-model concurrency;
- upstream concurrency.

Concurrency slot должен освобождаться в любом случае:

- normal completion;
- upstream error;
- client disconnect;
- timeout;
- internal gateway error.

Нельзя допускать утечки semaphore slot после оборванного streaming request.

## 12. Budget enforcement

Для API key должен поддерживаться денежный бюджет.

Примеры:

`$5 / day`

`$20 / month`

`$100 total`

Необходимо разделять:

- `spent`;
- `reserved`;
- `available`.

Так же как с токенами, budget должен резервироваться до отправки потенциально дорогого concurrent request и корректироваться после получения фактического usage.

Расчёт стоимости может использовать:

1. фактическую стоимость из upstream metadata, если 9router когда-либо её предоставляет;
2. локальную pricing table;
3. fallback стоимость 0/unknown с явным обозначением, что accounting incomplete.

Pricing table должна быть конфигурируемой по model pattern.

Пример логики:

`input_tokens × input_price + output_tokens × output_price`

Стоимость нельзя использовать как источник protocol behavior. Это отдельный accounting layer.

## 13. Hard limits во время streaming

Некоторые ограничения можно проверить только приблизительно до начала запроса. Поэтому gateway должен иметь возможность прервать уже выполняющийся request, если политика требует абсолютного hard cap.

Например:

- максимальное количество output tokens;
- абсолютный остаток бюджета;
- administrative kill.

При принудительном завершении gateway должен:

1. cancel upstream HTTP context;
2. прекратить чтение response;
3. закрыть downstream корректным способом;
4. записать причину termination;
5. сохранить доступный фактический usage.

Не нужно пытаться вставлять обычный JSON error в уже начатый SSE body.

Для streaming можно использовать корректный SSE error event только если это поддерживается клиентами; иначе безопаснее закрыть stream и оставить подробную причину в server logs/metrics.

## 14. Client cancellation

Это критическая функция для coding agents.

Если пользователь нажал Escape или OpenCode отменил generation, gateway должен максимально быстро отменить request в 9router.

Нельзя продолжать оплачиваемую генерацию после disconnect клиента.

В Go upstream request должен быть привязан к downstream `request.Context()`.

При cancellation необходимо:

- cancel upstream;
- release concurrency;
- снять неиспользованный reservation;
- записать aborted request;
- не считать такой request успешным.

## 15. Retry policy

Retry допускается только до того момента, когда downstream клиенту не отправлены meaningful response bytes.

До первого response chunk можно повторить запрос при:

- connection failure;
- selected 502/503/504;
- optionally 429 в рамках policy.

После начала streaming response автоматический retry запрещён. Повтор генерации после уже переданных токенов может привести к дублированию текста/tool calls и непредсказуемому поведению agent клиента.

Для MVP retries можно вообще не реализовывать. Надёжный прозрачный proxy лучше неправильного «умного» retry.

## 16. Observability и просмотр request body

Одна из полезных функций Bifrost, которую нужно сохранить — возможность увидеть, что клиент реально отправил.

Для каждого request желательно хранить:

- request ID;
- timestamp;
- gateway API key/name;
- endpoint;
- HTTP method;
- model;
- stream flag;
- response status;
- TTFT;
- total duration;
- upstream latency;
- input tokens;
- output tokens;
- total tokens;
- cost;
- termination reason;
- error;
- optionally request body;
- optionally response body.

Особенно полезно различать:

`Client Request Body`

и:

`Upstream Request Body`

На первом этапе они практически всегда будут одинаковыми, но это позволит в будущем видеть любые gateway transformations.

Body logging должен быть отключаемым и иметь ограничения по размеру.

Пример policies:

`log_request_body: true`

`log_response_body: false`

`max_logged_body_size: 256KB`

Обязательно скрывать:

- Authorization;
- gateway API keys;
- upstream API keys;
- потенциально другие configurable secrets.

Большие OpenCode prompts нельзя без ограничений складывать в SQLite. Для превышающих лимит body нужно хранить truncated content и original size.

## 17. Метрики latency

Проект должен отдельно измерять:

- total request duration;
- upstream connect latency;
- TTFT;
- time to final content;
- stream close delay;
- output tokens/sec.

`stream_close_delay` особенно важен из-за проблемы, ставшей причиной разработки проекта.

Определение:

время фактического завершения downstream stream минус время последнего meaningful/terminal upstream event.

Для нормального transparent proxy ожидаемое значение должно быть близко к нулю.

Необходимо создать regression test, который гарантирует отсутствие искусственной задержки порядка секунд.

## 18. Storage

Основная цель — отсутствие обязательного PostgreSQL/Redis.

Для single-instance deployment:

- runtime counters — RAM;
- persistent entities и агрегаты — SQLite;
- config — YAML/environment variables.

Минимальная установка:

`gateway binary + config.yaml + gateway.db`

SQLite должен использовать WAL mode.

Не нужно писать каждый SSE chunk в SQLite синхронно.

Request telemetry можно писать асинхронно через небольшую bounded queue.

При падении telemetry writer сам proxy не должен переставать пропускать requests, если quota enforcement может продолжить безопасную работу.

## 19. Configuration

Основная системная конфигурация должна быть понятной и маленькой.

Не нужно пытаться переносить в YAML все runtime API keys и usage counters.

Config отвечает за:

- listen address;
- upstream 9router URL;
- upstream secret;
- storage;
- global defaults;
- timeouts;
- logging;
- pricing;
- tokenizer strategy.

API key policies хранятся в SQLite и управляются через CLI/admin API.

Поддержать environment variable substitution для secrets.

## 20. Admin API и CLI

Web UI не является частью первого MVP.

Сначала требуется admin API и CLI.

Пример операций:

- create key;
- revoke key;
- enable/disable key;
- list keys;
- inspect policy;
- set RPM;
- set token limit;
- set concurrency;
- set budget;
- show usage;
- show recent requests.

CLI должен общаться либо напрямую с local database, либо предпочтительно с admin HTTP API.

Admin API должен использовать отдельный master/admin credential.

Нельзя позволять обычному gateway API key изменять собственные ограничения.

## 21. Web UI — после стабильного transport

Web UI следует реализовывать только после того, как proxy, streaming, accounting и limiter доказали стабильность.

Основные экраны будущего UI:

Dashboard: requests, tokens, spend, errors, active requests, latency.

API Keys: создание, отключение, limits, budget, allowed models.

Requests: список запросов с latency, model, usage, cost и status.

Request Inspector: client request, upstream request, response/error, headers с редактированными secrets.

Models/Pricing: локальная pricing table и model aliases, если они когда-либо понадобятся.

Settings: upstream URL, logging, retention, tokenizer mode.

UI не должен становиться отдельным тяжёлым Node/Next.js сервисом, если можно избежать этого. Предпочтительно встроенный frontend, компилируемый/embedding в Go binary.

## 22. Timeouts

Timeouts необходимо разделить:

- connection timeout;
- response headers timeout;
- non-stream request timeout;
- streaming idle timeout.

Нельзя устанавливать короткий total timeout на streaming generations.

Idle timeout нужен только для реально зависшего соединения, а не для определения того, закончила ли модель ответ.

Если upstream отправил EOF, downstream закрывается немедленно и никакой timeout больше не участвует.

## 23. Error handling

Ошибки gateway должны максимально соответствовать OpenAI-style API errors, но gateway не должен переписывать нормальные upstream errors без необходимости.

Нужно различать как минимум:

- invalid_api_key;
- key_disabled;
- rpm_limit_exceeded;
- token_limit_exceeded;
- concurrency_limit_exceeded;
- budget_exceeded;
- model_not_allowed;
- upstream_timeout;
- upstream_connection_error;
- upstream_error;
- gateway_internal_error.

Для debugging каждый error response должен иметь request ID.

Upstream HTTP status по возможности сохраняется.

Нельзя превращать все upstream проблемы в HTTP 500, как это произошло с Bifrost.

## 24. Health и metrics endpoints

Минимум:

`GET /health`

`GET /ready`

В будущем:

`GET /metrics`

в Prometheus format.

`/ready` должен учитывать возможность открыть storage и корректность основной конфигурации.

Проверять реальную доступность 9router на каждом readiness request не обязательно, чтобы временный upstream outage не делал сам gateway administratively unavailable.

## 25. Тестовый upstream

Для проекта обязателен собственный mock OpenAI-compatible upstream.

Он должен уметь искусственно воспроизводить нестандартные ситуации.

Test scenario 1: normal JSON response.

Test scenario 2: normal SSE с `[DONE]`.

Test scenario 3: SSE без `[DONE]`, затем EOF. Это соответствует реальному поведению 9router.

Test scenario 4: входящий `stream=false`, upstream возвращает SSE.

Test scenario 5: SSE terminal chunk, после которого upstream зависает.

Test scenario 6: tool_calls split между несколькими SSE events.

Test scenario 7: один SSE event разбит между несколькими TCP reads.

Test scenario 8: несколько SSE events приходят одним TCP read.

Test scenario 9: client disconnect.

Test scenario 10: два и больше параллельных requests.

Test scenario 11: upstream возвращает 429/500/502.

Test scenario 12: extremely large request body.

Нельзя предполагать, что граница `Read()` совпадает с границей SSE event.

## 26. Regression tests по реальным проблемам Bifrost

Проект должен иметь отдельные тесты, прямо защищающие от тех ошибок, из-за которых он появился.

Regression 1: non-stream request + upstream SSE не должен приводить к JSON unmarshal error.

Regression 2: terminal SSE/EOF не должен добавлять artificial end-of-stream latency.

Regression 3: streaming request не должен ждать несколько секунд перед downstream close.

Regression 4: два concurrent requests не должны сериализовываться без соответствующего configured limit.

Regression 5: client disconnect должен отменять upstream request.

Regression 6: limiter/accounting parser не должен блокировать passthrough streaming.

Эти тесты важнее количества поддерживаемых AI providers.

## 27. Производительность

Цель не состоит в достижении экстремального benchmark throughput, однако proxy overhead должен быть практически незаметным относительно LLM latency.

Для streaming основным критерием является отсутствие дополнительной буферизации.

Целевые значения для локальной Docker network при отсутствии limiter contention:

- дополнительный TTFT overhead: единицы миллисекунд;
- per-chunk flush delay: единицы миллисекунд;
- stream close delay: максимально близко к нулю;
- RAM footprint idle: небольшой;
- отсутствие обязательного Redis/Postgres.

Gateway не должен копировать multi-megabyte body большее количество раз, чем необходимо.

## 28. Язык и технологии

Предпочтительная реализация — Go.

Причины:

- хороший HTTP stack;
- дешёвая concurrency;
- `request.Context()` для cancellation;
- простой streaming;
- небольшой memory footprint;
- один статически компилируемый binary;
- удобный Docker image;
- SQLite доступен через зрелые библиотеки.

Не использовать сложный framework без необходимости.

Структура примерно:

`cmd/gateway`

`internal/proxy`

`internal/streaming`

`internal/auth`

`internal/limiter`

`internal/accounting`

`internal/tokenizer`

`internal/storage`

`internal/observability`

`internal/admin`

Главное требование — limiter/accounting не должен быть тесно связан с proxy transport.

## 29. Заимствование идей и кода Bifrost

Bifrost можно использовать как reference implementation для удачных функций:

- limiter semantics;
- token accounting;
- tokenizer;
- budget logic;
- request observability;
- request body inspection;
- API key policies.

Transport/streaming слой Bifrost копировать концептуально не следует, поскольку именно его поведение является одной из основных причин создания проекта.

Если конкретные части исходного кода Bifrost реально копируются или адаптируются, необходимо сохранить требуемые license/copyright notices и явно отметить изменения.

Предпочтительный подход: заимствовать проверенные алгоритмы и структуры данных там, где они хорошо отделены, но не переносить Bifrost architecture целиком.

## 30. MVP scope

Первая реально используемая версия должна включать:

1. OpenAI-compatible generic `/v1/*` reverse proxy.
2. `/v1/chat/completions` awareness.
3. API key authentication.
4. Per-key request limits.
5. Per-key concurrency limit.
6. Token accounting.
7. Basic token/time-window limits.
8. Transparent SSE streaming.
9. SSE → JSON aggregation для `stream=false`.
10. Client cancellation propagation.
11. SQLite storage.
12. Request metadata logging.
13. Optional request body logging.
14. Basic budget accounting.
15. Health endpoint.
16. CLI/admin API для keys и limits.
17. Integration test suite с mock upstream.
18. Docker image и docker-compose example.

Не входит в MVP:

- Web UI;
- multiple upstream routing;
- load balancing;
- automatic provider fallback;
- Anthropic API endpoint;
- OpenAI ↔ Anthropic conversion;
- distributed deployment;
- Redis;
- PostgreSQL;
- Kubernetes;
- complex retries;
- prompt caching;
- guardrails;
- plugins.

## 31. Предлагаемые этапы разработки

Phase 1 — Transport prototype. Сделать максимально простой reverse proxy `client → gateway → 9router`. Проверить OpenCode. Добиться идентичной субъективной скорости прямому 9router. Реализовать SSE passthrough, immediate flush и cancellation. На этом этапе никакой SQLite и budgets не нужны.

Phase 2 — Streaming compatibility. Добавить определение реального response type и SSE → non-stream aggregation. Реализовать корректные content/tool call accumulators. Добавить mock upstream и regression tests.

Phase 3 — Authentication and concurrency. API keys, model allow/deny, request limits и semaphore concurrency.

Phase 4 — Token accounting. Usage parser, estimator/tokenizer interface, token windows, reservations и reconciliation.

Phase 5 — Budget. Pricing table, cost calculation, spend windows, reservation, budget rejection.

Phase 6 — Persistence and observability. SQLite, aggregated usage, request history, optional request body inspector, metrics.

Phase 7 — Admin API/CLI.

Phase 8 — Web UI после стабилизации backend.

Нельзя начинать UI до того, как OpenCode стабильно работает через gateway без измеримого degradation относительно прямого 9router.

## 32. Definition of Done для первого usable release

Релиз можно считать пригодным к замене Bifrost только если выполняются все следующие условия.

OpenCode стабильно работает через gateway.

Streaming начинается практически с тем же TTFT, что напрямую через 9router.

После последнего upstream chunk gateway не добавляет секунд ожидания.

Non-stream client корректно работает даже если 9router force-stream'ит upstream.

Два параллельных request действительно выполняются параллельно при отсутствии concurrency limit.

RPM и concurrency limits реально блокируют превышение.

Token usage считается корректно на типичных OpenCode запросах.

Budget прекращает новые requests после достижения лимита.

Client cancellation отменяет upstream generation.

Request inspector позволяет понять, что конкретно отправил OpenCode.

Gateway работает без PostgreSQL и Redis.

После restart persistent keys/budgets/usage не пропадают.

Все реальные найденные ранее Bifrost regression scenarios покрыты automated tests.

## 33. Главная архитектурная установка для дальнейшей разработки

При любых будущих изменениях нужно задавать вопрос: «Обязан ли gateway вмешиваться в этот request/response?»

Если ответ «нет» — данные должны пройти напрямую.

Limiter может посчитать stream, но не должен задержать stream.

Logger может посмотреть body, но не должен изменить body.

Tokenizer может оценить request, но не должен переписывать request.

Accounting может увидеть terminal event, но не должен из-за этого удерживать HTTP connection.

Policy engine может остановить запрос, но не должен заниматься provider routing.

В идеальном случае gateway должен ощущаться как обычный прозрачный reverse proxy, к которому добавили API keys, счётчики и шлагбаум. Именно это отличает проект от тяжёлых AI gateway-комбайнов и является основной причиной его существования.
