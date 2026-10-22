# Issue #42: Додати команду /status у бота

Repo: masteine/relay
Labels: relay, relay:triage
Branch: relay/42-dodaty-komandu-status-u-bota
Spec path: docs/specs/42-dodaty-komandu-status-u-bota.md
Design: never
Owner: masteine

## Body

## Задача
Додати в бота Relay команду `/status`: у відповідь на неї бот повідомляє, чи
онлайн раннер кожного репо з конфігу, і які Issue зараз мають лейбл стану
`relay:*`, з етапом і часом, що минув від останнього коментаря Relay.

## Контекст
- Бот живе в `internal/bot/core`; транспорт — `internal/bot/transport`.
- Стан раннерів читається через GitHub API `GET /repos/{r}/actions/runners`.
- Активні задачі — Issue з лейблом `relay:*`, крім `relay:done`/`relay:failed`.
- Відповідь — один текст до 4096 символів, без кнопок.

## Критерії приймання
- [ ] `/status` у дозволеному чаті дає відповідь ≤ 5 с із рядком на кожне репо.
- [ ] Для офлайн-раннера рядок містить «офлайн».
- [ ] Для кожної активної задачі видно `#n`, етап і час від останнього коментаря Relay у хвилинах.
- [ ] Юніт-тести ядра з фейковим GitHub покривають: 0 репо, онлайн/офлайн, 0 і 2 активні задачі.

## Не в обсязі
Сторож-workflow, повідомлення після пробудження Mac.

## Comments

### #100 · masteine · 2026-10-21T15:00:00Z

todo: хочу бачити з телеграма, чи живий раннер і що зараз у роботі

### #101 · github-actions[bot] · 2026-10-21T15:02:10Z

## Relay · discuss

Зрозумів: одна команда `/status`, відповідь текстом. Два уточнення:
1. Показувати всі репо з конфігу чи лише ті, де є активні задачі? Я б показував усі.
2. Час на етапі рахувати від останнього коментаря Relay? Інакше його нема звідки взяти.

<!-- relay-ack {"v":1,"upto":100} -->
<!-- relay-usage {"v":1,"stage":"discuss","turns":4,"in":12000,"out":900,"cache_read":40000,"cache_create":50000,"ms":52000,"usd":0.07,"model":"sonnet"} -->
<!-- relay-session {"v":1,"id":"7c5a2d2e-4a2f-5e1b-9c3a-0f1e2d3c4b5a"} -->

### #102 · masteine · 2026-10-21T15:05:00Z

всі репо, час від останнього коментаря ок

### #103 · masteine · 2026-10-21T15:05:20Z

/ticket

### #104 · github-actions[bot] · 2026-10-21T15:06:40Z

## Relay · ticket

Задачу записано в тіло Issue. Далі: тріаж і план.

<!-- relay-ack {"v":1,"upto":103} -->
<!-- relay-usage {"v":1,"stage":"ticket","turns":2,"in":9000,"out":600,"cache_read":0,"cache_create":0,"ms":41000,"usd":0.03,"model":"sonnet"} -->
