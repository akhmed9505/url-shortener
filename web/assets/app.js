'use strict';

const $ = (id) => document.getElementById(id);
const numbers = new Intl.NumberFormat('ru-RU');
let createdLink = null;
let selected = null;
let nextCursor = null;
let firstPage = true;
let analyticsController = null;
let createBusy = false;
let analyticsBusy = false;
let visitsBusy = false;
let copyVersion = 0;
const cooldowns = { create: 0, analytics: 0 };

const apiErrorMessages = {
  invalid_url: 'Укажите абсолютный URL с http:// или https:// длиной до 2048 байт. Логин, пароль, пробелы, управляющие символы и обратные слеши запрещены.',
  invalid_alias: 'Код должен содержать от 3 до 32 латинских букв, цифр, подчёркиваний или дефисов.',
  invalid_code: 'Некорректный код ссылки. Допустимы от 3 до 32 латинских букв, цифр, подчёркиваний или дефисов.',
  invalid_period: 'Укажите обе даты и период от 1 до 366 дней. Конечная дата не включается.',
  invalid_cursor: 'Некорректный курсор. Загрузите аналитику заново.',
  invalid_limit: 'Количество переходов на странице должно быть от 1 до 100.',
  alias_taken: 'Этот код уже занят. Выберите другой.',
  link_not_found: 'Ссылка не найдена.',
  request_timeout: 'Время ожидания истекло. Повторите попытку.',
  storage_unavailable: 'Хранилище временно недоступно. Повторите попытку позже.',
  code_generation_exhausted: 'Не удалось сгенерировать код ссылки. Повторите попытку.',
  internal_error: 'Внутренняя ошибка сервера. Повторите попытку позже.',
  body_too_large: 'Запрос слишком большой. Максимальный размер — 4 КиБ.',
  invalid_json: 'Некорректный формат запроса.',
  unsupported_media_type: 'Сервер не поддерживает формат запроса.',
  not_found: 'Запрашиваемый адрес не найден.',
  method_not_allowed: 'Метод запроса не поддерживается.'
};

// Supply Russian validation messages independently of the browser's UI language.
for (const input of document.querySelectorAll('input')) {
  input.addEventListener('input', () => input.setCustomValidity(''));
  input.addEventListener('invalid', () => {
    if (input.validity.valueMissing) {
      input.setCustomValidity('Заполните это поле.');
    } else if (input.id === 'alias' || input.id === 'code') {
      input.setCustomValidity(apiErrorMessages.invalid_code);
    } else if (input.id === 'url') {
      input.setCustomValidity('Укажите корректный URL, например https://example.com.');
    } else {
      input.setCustomValidity('Укажите корректную дату.');
    }
  });
}

function message(id, text = '', state = '') {
  $(id).textContent = text;
  $(id).dataset.state = state;
}

function updateButtons() {
  const now = Date.now();
  $('create-button').disabled = createBusy || cooldowns.create > now;
  $('create-button').textContent = createBusy ? 'Создание…' : cooldowns.create > now ? 'Подождите…' : 'Сократить ссылку';
  $('analytics-button').disabled = analyticsBusy || cooldowns.analytics > now;
  $('analytics-button').textContent = analyticsBusy ? 'Загрузка…' : cooldowns.analytics > now ? 'Подождите…' : 'Показать аналитику';
  $('more-button').disabled = analyticsBusy || visitsBusy || cooldowns.analytics > now;
}

async function api(path, options, group, signal) {
  const timeout = AbortSignal.timeout(8000);
  const response = await fetch(path, { ...options, signal: signal ? AbortSignal.any([signal, timeout]) : timeout, headers: { Accept: 'application/json', ...options?.headers } });
  if (response.status === 429) {
    const header = response.headers.get('Retry-After');
    const seconds = /^\d+$/.test(header || '') ? Math.max(1, Number(header)) : 60;
    cooldowns[group] = Date.now() + seconds * 1000;
    updateButtons();
    throw new Error(`Слишком много запросов. Повторите через ${seconds} с.`);
  }
  let data;
  try {
    data = await response.json();
  } catch {
    throw new Error('Сервер вернул некорректный ответ. Повторите попытку.');
  }
  if (!response.ok) throw new Error(apiErrorMessages[data.error?.code] || 'Не удалось выполнить запрос. Повторите попытку.');
  return data;
}

function errorText(error) {
  if (error.name === 'TimeoutError') return 'Время ожидания истекло. Повторите попытку.';
  if (error instanceof TypeError) return 'Не удалось связаться с сервером. Проверьте соединение и повторите попытку.';
  return /[А-Яа-яЁё]/.test(error.message || '') ? error.message : 'Произошла ошибка. Повторите попытку.';
}

// Never assign API values as HTML. Links also receive an explicit protocol check.
function safeHTTPURL(value) {
  const parsed = new URL(value);
  if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) throw new Error('Сервер вернул некорректную ссылку.');
  return parsed.href;
}

$('create-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  if (createBusy || cooldowns.create > Date.now()) return;
  createBusy = true;
  createdLink = null;
  copyVersion++;
  $('copy-button').textContent = 'Копировать';
  $('create-result').hidden = true;
  message('create-message', 'Создание ссылки…');
  updateButtons();
  const payload = { url: $('url').value };
  // An unused optional field must be omitted, not sent as an empty alias.
  if ($('alias').value !== '') payload.alias = $('alias').value;
  try {
    const data = await api('/shorten', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) }, 'create');
    const href = safeHTTPURL(data.short_url);
    createdLink = { code: data.code, href };
    $('short-link').textContent = data.short_url;
    $('short-link').href = href;
    $('create-result').hidden = false;
    message('create-message', 'Ссылка создана.', 'success');
  } catch (error) {
    message('create-message', errorText(error), 'error');
  } finally {
    createBusy = false;
    updateButtons();
  }
});

$('copy-button').addEventListener('click', async () => {
  if (!createdLink) return;
  const version = ++copyVersion;
  const href = createdLink.href;
  try {
    if (!navigator.clipboard?.writeText) throw new Error('Буфер обмена недоступен');
    await navigator.clipboard.writeText(href);
    if (version !== copyVersion) return;
    $('copy-button').textContent = 'Скопировано';
    message('create-message', 'Короткая ссылка скопирована.', 'success');
  } catch {
    if (version !== copyVersion) return;
    message('create-message', 'Буфер обмена недоступен. Скопируйте выделенную ссылку вручную.', 'error');
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents($('short-link'));
    selection.removeAllRanges();
    selection.addRange(range);
  }
});

$('view-button').addEventListener('click', () => {
  if (!createdLink) return;
  $('code').value = createdLink.code;
  $('code').setCustomValidity('');
  $('analytics-title').scrollIntoView({ block: 'start' });
  $('analytics-form').requestSubmit();
});

function rows(id, values, columns, emptyText) {
  const fragment = document.createDocumentFragment();
  for (const value of values) {
    const row = document.createElement('tr');
    for (const column of columns) {
      const cell = document.createElement('td');
      cell.textContent = column(value);
      row.append(cell);
    }
    fragment.append(row);
  }
  if (values.length === 0 && emptyText) {
    const row = document.createElement('tr');
    const cell = document.createElement('td');
    cell.colSpan = columns.length;
    cell.textContent = emptyText;
    row.append(cell);
    fragment.append(row);
  }
  $(id).append(fragment);
}

function renderSummary(data) {
  $('summary-code').textContent = data.code;
  $('summary-url').textContent = data.url;
  $('summary-period').textContent = `${data.from} → ${data.to} (конечная дата не включается), UTC`;
  $('total-visits').textContent = numbers.format(data.total_visits);
  $('period-visits').textContent = numbers.format(data.period_visits);
  $('daily').replaceChildren();
  rows('daily', data.daily, [(item) => item.date, (item) => numbers.format(item.count)]);
  $('monthly').replaceChildren();
  rows('monthly', data.monthly, [(item) => item.month, (item) => numbers.format(item.count)]);
  $('user-agents').replaceChildren();
  rows('user-agents', data.user_agents, [(item) => item.user_agent || '(не указан)', (item) => numbers.format(item.count)], 'За выбранный период переходов нет.');
  $('other-agents').textContent = numbers.format(data.other_user_agents_visits);
  $('analytics-result').hidden = false;
  $('analytics-empty').hidden = true;
}

async function loadVisits(signal) {
  if (!selected || visitsBusy || cooldowns.analytics > Date.now()) return;
  visitsBusy = true;
  updateButtons();
  message('visits-message', 'Загрузка переходов…');
  $('more-button').textContent = 'Загрузка…';
  const query = new URLSearchParams({ from: selected.from, to: selected.to, limit: '20' });
  if (nextCursor) query.set('cursor', nextCursor);
  try {
    const data = await api(`/analytics/${encodeURIComponent(selected.code)}/visits?${query}`, {}, 'analytics', signal);
    if (signal.aborted) return;
    if (firstPage) $('visits').replaceChildren();
    rows('visits', data.items, [(item) => item.id, (item) => item.visited_at.replace('T', ' ').replace('Z', ' UTC'), (item) => item.user_agent || '(не указан)'], firstPage ? 'За выбранный период переходов нет.' : '');
    firstPage = false;
    nextCursor = data.next_cursor;
    $('more-button').hidden = !nextCursor;
    $('more-button').textContent = 'Загрузить ещё';
    message('visits-message', nextCursor ? 'Есть ещё переходы.' : 'Все переходы загружены.');
  } catch (error) {
    if (signal.aborted) return;
    message('visits-message', errorText(error), 'error');
    $('more-button').hidden = false;
    $('more-button').textContent = 'Повторить загрузку';
  } finally {
    if (!signal.aborted) {
      visitsBusy = false;
      updateButtons();
    }
  }
}

$('analytics-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  if (analyticsBusy || cooldowns.analytics > Date.now()) return;
  const code = $('code').value;
  const from = $('from').value;
  const to = $('to').value;
  const days = (Date.parse(to) - Date.parse(from)) / 86400000;
  if (!Number.isFinite(days) || days <= 0 || days > 366) {
    message('analytics-message', 'Выберите период от 1 до 366 дней. Конечная дата не включается.', 'error');
    return;
  }
  // Discard results from any page request belonging to the previous selection.
  analyticsController?.abort();
  visitsBusy = false;
  const controller = new AbortController();
  analyticsController = controller;
  analyticsBusy = true;
  selected = null;
  nextCursor = null;
  firstPage = true;
  $('analytics-result').hidden = true;
  $('analytics-empty').hidden = true;
  $('more-button').hidden = true;
  $('visits').replaceChildren();
  message('analytics-message', 'Загрузка аналитики…');
  message('visits-message');
  updateButtons();
  try {
    const query = new URLSearchParams({ from, to });
    const data = await api(`/analytics/${encodeURIComponent(code)}?${query}`, {}, 'analytics', controller.signal);
    renderSummary(data);
    selected = { code: data.code, from: data.from, to: data.to };
    message('analytics-message');
    await loadVisits(controller.signal);
  } catch (error) {
    if (!controller.signal.aborted) {
      message('analytics-message', errorText(error), 'error');
      $('analytics-empty').hidden = false;
    }
  } finally {
    analyticsBusy = false;
    updateButtons();
  }
});

$('more-button').addEventListener('click', () => {
  if (!analyticsBusy && analyticsController) loadVisits(analyticsController.signal);
});

const today = new Date();
const end = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() + 1));
const start = new Date(end);
start.setUTCDate(start.getUTCDate() - 30);
$('from').value = start.toISOString().slice(0, 10);
$('to').value = end.toISOString().slice(0, 10);
setInterval(updateButtons, 1000);
