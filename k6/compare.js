// Нагрузочное сравнение алгоритмов на настоящем HTTP-сервисе (ratelab serve).
// Каждому алгоритму отдельный сценарий: постоянный поток RATE запросов/с в течение SECONDS секунд.
// Сервис по умолчанию настроен на 100 запросов/с, поэтому при RATE=200 половина запросов должна отклоняться.
//
// Запуск:  ratelab serve --addr 127.0.0.1:8080 &   затем   k6 run k6/compare.js
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const BASE = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const RATE = Number(__ENV.RATE || 200);
const SECONDS = Number(__ENV.SECONDS || 5);
const MIN = Number(__ENV.MIN_ALLOWED || 450);
const MAX = Number(__ENV.MAX_ALLOWED || 700);
const ALGOS = ['fixed-window', 'sliding-log', 'sliding-counter', 'token-bucket', 'gcra'];

const metric = (a) => a.replace(/-/g, '_');
const allowed = {};
const denied = {};
for (const a of ALGOS) {
  allowed[a] = new Counter('allowed_' + metric(a));
  denied[a] = new Counter('denied_' + metric(a));
}

const scenarios = {};
const thresholds = { http_req_failed: ['rate<0.001'] }; // 429 не считается ошибкой, см. setResponseCallback
ALGOS.forEach((a, i) => {
  scenarios[metric(a)] = {
    executor: 'constant-arrival-rate',
    rate: RATE,
    timeUnit: '1s',
    duration: `${SECONDS}s`,
    preAllocatedVUs: 50,
    maxVUs: 200,
    startTime: `${i * (SECONDS + 3)}s`, // сценарии идут по очереди, чтобы не мешать друг другу
    exec: 'hit',
    env: { ALGO: a },
  };
  // Принятых запросов должно быть примерно «лимит × время» плюс пачка; границы широкие, чтобы тест не был хрупким
  thresholds['allowed_' + metric(a)] = [`count>=${MIN}`, `count<=${MAX}`];
});

http.setResponseCallback(http.expectedStatuses(200, 429));
export const options = { scenarios, thresholds, summaryTrendStats: ['avg', 'p(95)', 'p(99)', 'max'] };

export function hit() {
  const algo = __ENV.ALGO;
  const res = http.get(`${BASE}/limited/${algo}`, { tags: { algo } });
  const ok = check(res, { 'ответ 200 или 429': (r) => r.status === 200 || r.status === 429 });
  if (!ok) return;
  if (res.status === 200) allowed[algo].add(1);
  else {
    denied[algo].add(1);
    check(res, { '429 содержит Retry-After': (r) => Number(r.headers['Retry-After']) >= 1 });
  }
}

export function handleSummary(data) {
  const v = (name) => (data.metrics[name] ? data.metrics[name].values.count : 0);
  const lines = ['', `Нагрузка ${RATE} запросов/с по ${SECONDS} с на алгоритм (лимит сервиса: 100/с)`, ''];
  lines.push('алгоритм          принято  отклонено');
  for (const a of ALGOS) lines.push(`${a.padEnd(16)} ${String(v('allowed_' + metric(a))).padStart(8)} ${String(v('denied_' + metric(a))).padStart(10)}`);
  const d = data.metrics.http_req_duration;
  if (d) lines.push('', `задержка p95 ${d.values['p(95)'].toFixed(2)} мс, p99 ${d.values['p(99)'].toFixed(2)} мс`);
  lines.push('');
  return { stdout: lines.join('\n'), 'k6-summary.json': JSON.stringify(data, null, 2) };
}
