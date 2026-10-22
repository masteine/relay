# Issue #42: Додати команду /status у бота

Issue: #42 · Статус: pending approval · Ітерацій огляду: 3 · Вердикт: APPROVE

## Вимоги

1. Власник репозиторію (або учасник з доступом) може написати в дозволений Telegram-чат `/status` і отримати відповідь, що показує: по кожному репозиторію з конфігурації бота — чи є вільні self-hosted runner'и, і для кожної відкритої (не `relay:done`/`relay:failed`) задачі з лейблами `relay:*` — номер, заголовок, поточний стан (`relay:*` лейбл) і короткий витяг останнього коментаря Relay (стадії).
2. `/status` — це запит-відповідь: один виклик боту повертає один знімок стану. Жодного фонового опитування (polling) GitHub бот не веде.
3. Бот підтримує декілька репозиторіїв (на відміну від `relay.yml`, який описує один проєкт) і обмежений список дозволених Telegram chat ID.
4. Рішення піднято з v1 у v0 власником репозиторію через коментар до issue #42 (#102 «всі репо, час від останнього коментаря ок») під час triage; розмір L.

## Принципи

1. Не множити абстракції: `/status` використовує той самий `gh.Client`, яким вже користуються стадії пайплайна (`internal/gh/gh.go`), а не окремий GitHub-клієнт для бота.
2. Кожен метод `gh.Client` — один виклик API (задокументовано в `internal/gh/gh.go:61-63`); `/status` не повинен порушувати це правило власною пагінацією в бізнес-логіці.
3. Фейк для тестів — один: існуючий `gh.Fake` (`internal/gh/fake.go`). Нова функціональність додає поля/методи до нього, а не створює другий тип.
4. Конфігурація бота (декілька репозиторіїв, дозволені чати) — окрема модель даних від `config.Project` (`internal/config/config.go:34-44`), яка описує один проєкт для одного запуску пайплайна.
5. `/status` не тримає стан між викликами: кожен запит — свіжий `Issue()`/`OpenIssues()`/`ListRunners()` до GitHub, без кешу і без фонових горутин.

## Драйвери рішення

1. **Відсутність двох методів у `gh.Client`.** Інтерфейс (`internal/gh/gh.go:63-77`) сьогодні не має способу (а) дізнатися про доступні runner'и, (б) перелічити відкриті задачі пайплайна. Обидва — природне розширення вже існуючого інтерфейсу, не нова підсистема.
2. **Відсутність конфігурації бота.** `internal/config/config.go` — проєктний конфіг, прив'язаний до одного репозиторію і одного запуску; бот працює з кількома репозиторіями й має власний список дозволених чатів.
3. **Відсутність Telegram-транспорту.** Жодної бібліотеки чи коду для Telegram у репозиторії немає (`go.mod` підтверджує відсутність будь-якого telegram-пакета).

## Варіанти

**Факт, що не є рішенням (GitHub-клієнт):** `internal/gh/github.go` вже реалізує `gh.Client` через `google/go-github/v93` (`go.mod`), з токеном через `GITHUB_TOKEN`/`GH_TOKEN` або `gh auth token` (`internal/gh/github.go:33-52`), протестовано в `internal/gh/gh_test.go`. Це єдиний GitHub-клієнт у проєкті. «go-github vs exec.Command(gh) vs сирий net/http» — не рішення, яке приймається цим планом: `/status` просто додає два методи до вже існуючого інтерфейсу й реалізації.

**Рішення А — статус runner'ів:**
- Варіант 1 (обрано) — `ListRunners` у `gh.Client`, один виклик без повної пагінації (`a.gh.Actions.ListRunners(..., ListOptions{PerPage:100})`). Переваги: мінімум коду, узгоджено з принципом «один метод — один виклик». Недолік: пропустить runner'и понад 100 — прийнятно, бо типовий флот self-hosted runner'ів Relay — одна-дві машини; follow-up задокументовано.
- Варіант 2 — повна пагінація одразу, як в `Issue()`. Безпечніше на зріст, але зайва складність під неіснуючий зараз розмір флоту.

**Рішення Б — список відкритих задач:**
- Варіант 1 (обрано) — `OpenIssues` з серверною фільтрацією через `labels=["relay"]` (`protocol.LabelPipeline`), далі клієнтське відкидання `relay:done`/`relay:failed` через `protocol.ParseLabels`. Дешевше за виклики API, лейбл `relay` завжди присутній на задачах пайплайна.
- Варіант 2 — забрати всі відкриті issues і фільтрувати клієнтською стороною за `protocol.AllStates`. Простіше, але витрачає виклики API на issues, які Relay не торкався.

**Пошук останнього коментаря Relay — не рішення:** `gh.Client.Issue(ctx, number)` (`internal/gh/gh.go:67`, реалізація `internal/gh/github.go:62-100`) вже повертає всі коментарі, повністю пагіновані і відсортовані за зростанням `ID` (`Issue.Protocol()`, `internal/gh/gh.go:52-59`). `StatusService` просто бере `Comments` з кінця й шукає перший, для якого `protocol.IsRelayComment(c.Body)` (`internal/protocol/comments.go:40-43`) істинний, потім розбирає через `protocol.ParseStageComment(c.Body)` (`internal/protocol/comments.go:278-315`). Нового коду пагінації ніде немає.

**Рішення Г — пакет для конфігурації бота:**
- Варіант 1 — `internal/config/bot.go` (той самий пакет, що й проєктний конфіг). Мінус: змішує дві непов'язані моделі в одному неймспейсі.
- Варіант 2 (обрано) — `internal/bot/config.go`, новий пакет. Причина: `BotConfig` не є варіацією `relay.yml`, а документація `internal/config/config.go:1-3` явно звужує пакет до «per-project configuration».

**Рішення Д — бібліотека Telegram:**
- Варіант 1 (обрано) — `go-telegram/bot`: контекстний (`context.Context` у кожному хендлері), узгоджується зі стилем `gh.Client`, активно підтримується.
- Варіант 2 — `go-telegram-bot-api/telegram-bot-api`: широко вживана, але старіший API без наскрізного `context.Context`.

## Рішення (ADR)

**Рішення.** `/status` реалізується як: (1) два нові методи в існуючому `gh.Client` (`internal/gh/gh.go`): `ListRunners` (без повної пагінації, з коментарем-поясненням) і `OpenIssues` (серверна фільтрація за лейблом `relay`, клієнтське відкидання `relay:done`/`relay:failed`), реалізовані в `internal/gh/github.go` (`API`) і `internal/gh/fake.go` (`Fake`); (2) пошук останнього коментаря-стадії — існуюча функціональність `Issue()` + `protocol.IsRelayComment`/`protocol.ParseStageComment`, без нового коду пагінації; (3) нова конфігурація бота в `internal/bot/config.go` (не в `internal/config`), з багатьма репозиторіями й списком дозволених chat ID; (4) новий транспортний шар на `go-telegram/bot` у `internal/bot/transport` і `internal/bot/telegram`, і точка входу `cmd/relay/bot.go`; (5) `StatusService` (`internal/bot/core`) — чистий request/response сервіс без фонових горутин.

**Драйвери:** не дублювати вже готовий і протестований `gh.Client`/`API`/`Fake`; закрити дві реальні відсутні можливості (runner'и, список відкритих задач) мінімальним розширенням існуючого API; не змішувати одно-проєктну конфігурацію з багато-репозиторною конфігурацією бота.

**Розглянуті альтернативи:** окремий GitHub-клієнт для бота (відхилено — немає підстав дублювати `gh.Client`, це була помилка попередньої версії плану, знайдена архітектором); повна пагінація `ListRunners` одразу (відхилено для MVP, задокументовано як follow-up); клієнтська фільтрація всіх відкритих issues без серверного `labels`-параметра (відхилено — дорожче без потреби); `BotConfig` в `internal/config` (відхилено — порушує звужену документовану відповідальність пакета).

**Чому обрано:** усі рішення мінімізують новий код, розширюючи вже протестовані абстракції замість створення паралельних, і явно розділяють одно-проєктну та багато-репозиторну конфігурацію.

**Наслідки:** `gh.Client` росте на два методи — лише дві реалізації (`API`, `Fake`) потребують оновлення; `internal/bot` — новий пакет-дерево без питань зворотної сумісності.

**Подальші кроки:** якщо флот self-hosted runner'ів колись перевищить 100 машин — додати цикл пагінації в `ListRunners` за зразком `Issue()` (`internal/gh/github.go:81-98`); `OpenIssues` і фільтр за `protocol.AllStates` вже готові для розширення `/status` на v1-стадії без нової роботи з GitHub API.

## Кроки імплементації

1. `internal/gh/gh.go`: додати тип `Runner{ID int64; Name string; OS string; Status string; Busy bool; Labels []string}` і метод інтерфейсу `Client`: `ListRunners(ctx context.Context) ([]Runner, error)`, doc-коментар пояснює відсутність пагінації.
2. `internal/gh/github.go`: реалізувати `(a *API) ListRunners` через `a.gh.Actions.ListRunners(ctx, a.repo.Owner, a.repo.Name, &github.ListRunnersOptions{ListOptions: github.ListOptions{PerPage: 100}})`, один виклик без циклу `NextPage`, мапінг `*github.Runner` → `gh.Runner`.
3. `internal/gh/fake.go`: нове поле `Runners []Runner` на `Fake`, метод `(f *Fake) ListRunners` повертає `f.Runners`, перевіряє `f.Fail` (за зразком `IsPrivate`, рядки 38-43).
4. Підтвердити, що `gh.Issue.Labels` (`internal/gh/gh.go:46`) достатньо для `protocol.ParseLabels(issue.Labels)` (`internal/protocol/labels.go:69`) — змін у `gh.Issue` не потрібно.
5. `internal/gh/gh.go`: додати метод інтерфейсу `OpenIssues(ctx context.Context) ([]Issue, error)`, doc-коментар: відкриті issues з лейблом `relay` (`protocol.LabelPipeline`), крім стану `relay:done`/`relay:failed`.
6. `internal/gh/github.go`: реалізувати через `a.gh.Issues.ListByRepo(ctx, owner, name, &github.IssueListByRepoOptions{State:"open", Labels:[]string{"relay"}, ListOptions: github.ListOptions{PerPage:100}})` з циклом пагінації `NextPage` (за зразком `Issue()`, рядки 81-98 — тут пагінація виправдана, кількість відкритих задач пайплайна реально може перевищити 100), для кожного issue — викликати `a.Issue(ctx, is.GetNumber())` для коментарів, фільтрувати через `protocol.ParseLabels` + перевірку стану, пропускати `IsPullRequest`.
7. `internal/gh/fake.go`: `(f *Fake) OpenIssues` ітерує `f.Issues` під `f.mu`, фільтрує через `protocol.ParseLabels`, відкидає `done`/`failed`, повертає копії, перевіряє `f.Fail`.
8. `internal/gh/gh_test.go`: новий тест `TestOpenIssuesFiltersDoneAndFailed` через `gh.NewFake(...)` з issues у різних станах і без лейблу `relay`.
9. `internal/bot/config.go` (новий файл, новий пакет `bot`): `BotConfig{Repos []gh.Repo; AllowedChats []int64}`, `LoadBot(path string) (BotConfig, error)` — YAML через `gopkg.in/yaml.v3`, `repos` парситься через `gh.ParseRepo` (`internal/gh/gh.go:25-31`), валідація: ≥1 repo, ≥1 дозволений чат.
10. `internal/bot/config_test.go`: коректний парсинг, помилка при порожніх `repos`, помилка при порожніх `allowed_chats`, помилка при некоректному `owner/name`.
11. `internal/bot/core/status.go` (новий файл, пакет `core`): `StatusService{Clients map[gh.Repo]gh.Client}`, метод `Status(ctx context.Context) (Report, error)` — для кожного репо: `ListRunners`+`OpenIssues`, для кожного issue — пошук останнього коментаря Relay з кінця `Comments`; `Report{Repos []RepoStatus}`, `RepoStatus{Repo gh.Repo; FreeRunners int; Busy []Runner; Tasks []TaskStatus; Err error}`, `TaskStatus{Number int; Title string; State protocol.State; LastStage protocol.Stage; LastStageExcerpt string}`.
12. `internal/bot/core/status_test.go`: fake issue з коментарями, де останній за ID — не-Relay коментар людини, перед ним — Relay-коментар стадії `plan`, раніше — Relay-коментар стадії `triage`; перевірка `LastStage == protocol.StagePlan`.
13. Той самий файл: задачі в станах `plan`/`done`/`failed`/`implement` + runners з частиною `Busy:true`; перевірка, що `Report` містить рівно 2 активні задачі (`plan`, `implement`) і правильний рахунок вільних runner'ів.
14. Той самий файл: один репозиторій з `Fake.Fail` — перевірка, що `Status` повертає помилку для цього репозиторію в `RepoStatus.Err`, не обриваючи обробку інших репозиторіїв.
15. Той самий файл: два послідовні виклики `Status(ctx)` з незмінним `Fake` дають побайтово ідентичний `Report` (`reflect.DeepEqual` або рендер в текст і порівняння рядків) — основна перевірка відсутності self-polling. `runtime.NumGoroutine()` НЕ входить до критеріїв приймання (флакі через GC/keep-alive з'єднання go-github); може бути допоміжним `t.Logf`, не блокуючим.
16. `internal/bot/core/render.go` (новий файл): `RenderStatus(r Report) string` — текст у форматі Telegram Markdown: репозиторій → кількість вільних runner'ів → список задач (номер, заголовок, стан, витяг останньої стадії до ~200 символів).
17. `internal/bot/core/render_test.go`: `RenderStatus` на фіксованому `Report` — перевірка ключових підрядків (номер задачі, назва стану, обрізаний витяг).
18. `go get github.com/go-telegram/bot@latest`, зафіксувати версію в `go.mod` після виконання.
19. `internal/bot/telegram/handler.go` (новий файл, новий пакет): хендлер `/status` перевіряє chat ID проти `BotConfig.AllowedChats`, викликає `StatusService.Status`, рендерить через `core.RenderStatus`, відсилає через `bot.Bot.SendMessage`; неавторизований chat ID — тиха відмова (без відповіді, лог попередження).
20. `cmd/relay/bot.go` (новий файл): читає `TELEGRAM_BOT_TOKEN` з env, `BotConfig` через `bot.LoadBot` (шлях з флага/env), для кожного `gh.Repo` створює `gh.API` через `gh.New`+`gh.Token()` (`internal/gh/github.go:22-52`), складає `StatusService.Clients`, запускає Telegram long-polling (`bot.Bot.Start`), graceful shutdown на `SIGINT`/`SIGTERM`.
21. Оновити `README.md` (чи `internal/bot/README.md`) коротким описом формату конфігурації бота (`repos`, `allowed_chats`) і команди `/status`.

## Критерії приймання

1. `go build ./...` — exit 0 після додавання `ListRunners`/`OpenIssues` до `gh.Client`/`API`/`Fake`.
2. `go test ./internal/gh/...` — усі тести зелені, включно з `TestOpenIssuesFiltersDoneAndFailed`.
3. `go test ./internal/bot/...` — усі тести зелені (`StatusService`, `LoadBot`, `RenderStatus`).
4. `go test -run TestStatusService -count=5 ./internal/bot/core/...` — 5/5 успішних прогонів, підтверджуючи детермінованість відповіді без self-polling.
5. Ручна перевірка: `/status` з chat ID зі списку `AllowedChats` повертає повідомлення зі списком репозиторіїв, кількістю вільних runner'ів і списком відкритих задач з їхнім останнім станом.
6. Ручна перевірка: `/status` з чату поза `AllowedChats` не отримує відповіді (тиха відмова).
7. `gofmt -l .` — порожній вивід.

## Ризики і запобіжники

| Ризик | Запобіжник |
|---|---|
| `ListRunners` без пагінації пропустить runner'и понад 100 машин | Явний doc-коментар + follow-up у ADR; задокументовано як свідомий вибір для типового розміру флоту |
| `OpenIssues` робить по одному зайвому виклику `Issue()` на кожну відкриту задачу, що при великій кількості задач сповільнить `/status` | Прийнятно для MVP (типово одиниці-десятки активних задач); follow-up, якщо стане проблемою |
| Нова конфігурація бота (`internal/bot/config.go`) дублює парсинг YAML і дає розбіжність зі стилем `internal/config/config.go` | Обидва використовують `gopkg.in/yaml.v3` і стиль «DecodeKnownFields + Validate», звірено в кроці 10 |
| Вибір `go-telegram/bot` виявиться непридатним після інтеграції | Увесь виклик Telegram API ізольований у `internal/bot/telegram`; `core.StatusService`/`RenderStatus` не залежать від бібліотеки — заміна торкається лише одного пакета |
| Неавторизований доступ до `/status` розкриє дані приватного репозиторію стороннім чатам | Перевірка `AllowedChats` на вході хендлера до будь-якого звернення до `gh.Client`, тиха відмова без підтвердження існування команди |
| Флакі-тест «no self-polling» через `runtime.NumGoroutine()` | Прибрано з критеріїв приймання; основна перевірка — детермінована побітова рівність двох послідовних `Report` (крок 15, AC4) |
| Помилка одного репозиторію (мережа, 404, revoked token) обвалює всю відповідь `/status` | `StatusService.Status` ізолює помилку в `RepoStatus.Err`, інші репозиторії обробляються незалежно (крок 14) |

## Перевірка

1. `go build ./...` — exit 0.
2. `go vet ./...` — exit 0.
3. `gofmt -l .` — порожній вивід.
4. `go test ./internal/gh/... -v` — усі `PASS`, включно з `TestOpenIssuesFiltersDoneAndFailed`.
5. `go test ./internal/bot/... -v` — усі `PASS`.
6. `go test -run TestStatusService -count=5 ./internal/bot/core/...` — 5 запусків, усі `PASS`.
7. Ручна перевірка: запустити `cmd/relay/bot.go` локально з тестовим `TELEGRAM_BOT_TOKEN` і конфігурацією на один репозиторій, надіслати `/status` з дозволеного й недозволеного chat ID, підтвердити відповідність критеріям приймання 5 і 6.

## Журнал огляду

Раунд 1 — архітектор: питання щодо майбутнього сторожа (watchdog) і повторного використання `StatusService`; критик: REVISE (3 пункти — тест відсутності self-polling, перенесення ADR-нотатки про свідоме випередження v1, тест пагінації коментарів); зміни застосовано: додано 6-й тест, ADR-нотатка про рішення власника (issue #102), тест на 35 коментарів/2 сторінки.

Раунд 2 — архітектор: виявив хибну передумову всього плану — `internal/gh` вже існує, вже реалізує `gh.Client` через `go-github v93`, уже коректно пагінує коментарі в `Issue()`; також відзначив `runtime.NumGoroutine()` як нестабільний сигнал тесту; зміни застосовано: видалено вигаданий вибір GitHub-клієнта з нуля, додано два реальні рішення (розширення `gh.Client` методами `ListRunners`/`OpenIssues`), прибрано зайвий тест пагінації коментарів (її вже нема потреби тестувати окремо — вона вже існує і протестована), прибрано `NumGoroutine()` з критеріїв приймання.

Раунд 3 — критик: APPROVE. Підтверджено, що передумова виправлена, усі посилання на файли/рядки коректні, кожен ризик має запобіжник, критерії приймання перевірювані командами чи спостереженнями.
