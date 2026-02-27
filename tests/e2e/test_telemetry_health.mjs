// test_telemetry_health.mjs
// Validates the telemetry pipeline: health endpoints, Prometheus targets, Grafana dashboards

const PROMETHEUS_URL = 'http://localhost:9090';
const GRAFANA_URL = 'http://localhost:3000';
const API_GATEWAY_URL = 'http://localhost:8080';
const MEDIA_SERVICE_URL = 'http://localhost:8090';

// NOTE: message-consumer (8091) does NOT have a Kubernetes Service resource — it is a pure
// Kafka consumer with no external port. To test its health endpoints you would need
// `kubectl port-forward`. api-gateway (8080) and media-service (8090) both have LoadBalancer
// Services and are reachable from the host.

let passed = 0;
let failed = 0;

function assert(condition, message) {
  if (condition) {
    console.log(`  PASS: ${message}`);
    passed++;
  } else {
    console.error(`  FAIL: ${message}`);
    failed++;
  }
}

async function safeFetch(url, options = {}) {
  try {
    return await fetch(url, options);
  } catch (err) {
    return { ok: false, status: 0, _fetchError: err.message };
  }
}

async function testHealthEndpoints() {
  console.log('\n--- Health Endpoints ---');

  // api-gateway: has a LoadBalancer Service on port 8080 — reachable from host
  const healthRes = await safeFetch(`${API_GATEWAY_URL}/healthz`);
  assert(healthRes.status === 200, `api-gateway GET /healthz returns 200 (got ${healthRes.status})`);

  if (healthRes.status === 200) {
    let body;
    try { body = await healthRes.json(); } catch (_) { body = null; }
    assert(body !== null && body.status === 'ok', `api-gateway /healthz body contains {"status":"ok"}`);
  } else {
    failed++;
    console.error('  FAIL: api-gateway /healthz body contains {"status":"ok"} (skipped — non-200 response)');
  }

  const readyRes = await safeFetch(`${API_GATEWAY_URL}/readyz`);
  assert(readyRes.status === 200, `api-gateway GET /readyz returns 200 (got ${readyRes.status})`);

  // media-service: has LoadBalancer Service on port 8090 (media-service-svc.yaml)
  const msHealthRes = await safeFetch(`${MEDIA_SERVICE_URL}/healthz`);
  assert(msHealthRes.status === 200, `media-service GET /healthz returns 200 (got ${msHealthRes.status})`);

  if (msHealthRes.status === 200) {
    let body;
    try { body = await msHealthRes.json(); } catch (_) { body = null; }
    assert(body !== null && body.status === 'ok', `media-service /healthz body contains {"status":"ok"}`);
  } else {
    failed++;
    console.error('  FAIL: media-service /healthz body contains {"status":"ok"} (skipped — non-200 response)');
  }

  const msReadyRes = await safeFetch(`${MEDIA_SERVICE_URL}/readyz`);
  assert(msReadyRes.status === 200, `media-service GET /readyz returns 200 (got ${msReadyRes.status})`);

  // message-consumer: pure Kafka consumer, no LoadBalancer Service — health only via kubectl port-forward
  console.log('  SKIP: message-consumer /healthz — no LoadBalancer Service (pure Kafka consumer)');
}

async function testPrometheusTargets() {
  console.log('\n--- Prometheus Scrape Targets ---');

  const res = await safeFetch(`${PROMETHEUS_URL}/api/v1/targets`);
  assert(res.status === 200, `Prometheus GET /api/v1/targets returns 200 (got ${res.status})`);

  if (res.status !== 200) {
    console.error('  SKIP: remaining Prometheus target checks (non-200 response)');
    failed += 3; // postgres, redpanda, minio
    return;
  }

  let data;
  try {
    data = await res.json();
  } catch (err) {
    assert(false, `Prometheus /api/v1/targets response is valid JSON (parse error: ${err.message})`);
    failed += 3;
    return;
  }

  assert(data.status === 'success', `Prometheus response has status "success" (got "${data.status}")`);

  const activeTargets = (data.data && data.data.activeTargets) ? data.data.activeTargets : [];
  const requiredJobs = ['postgres', 'redpanda', 'minio'];

  for (const job of requiredJobs) {
    const targets = activeTargets.filter(t => t.labels && t.labels.job === job);
    const healthy = targets.some(t => t.health === 'up');
    assert(healthy, `Prometheus scrape target "${job}" has health "up" (found ${targets.length} target(s))`);
  }
}

async function testGrafanaDashboards() {
  console.log('\n--- Grafana Dashboards ---');

  const res = await safeFetch(`${GRAFANA_URL}/api/search?type=dash-db`);
  assert(res.status === 200, `Grafana GET /api/search returns 200 (got ${res.status})`);

  if (res.status !== 200) {
    console.error('  SKIP: remaining Grafana dashboard checks (non-200 response)');
    failed += 4; // platform-overview, go-services, kafka-redpanda, media-saga
    return;
  }

  let dashboards;
  try {
    dashboards = await res.json();
  } catch (err) {
    assert(false, `Grafana /api/search response is valid JSON (parse error: ${err.message})`);
    failed += 4;
    return;
  }

  assert(Array.isArray(dashboards), `Grafana dashboard search returns an array (got ${typeof dashboards})`);

  const requiredUids = ['platform-overview', 'go-services', 'kafka-redpanda', 'media-saga'];
  const provisionedUids = Array.isArray(dashboards) ? dashboards.map(d => d.uid) : [];

  for (const uid of requiredUids) {
    assert(provisionedUids.includes(uid), `Grafana dashboard "${uid}" is provisioned`);
  }
}

async function testGrafanaDatasources() {
  console.log('\n--- Grafana Datasources ---');

  const res = await safeFetch(`${GRAFANA_URL}/api/datasources`);
  assert(res.status === 200, `Grafana GET /api/datasources returns 200 (got ${res.status})`);

  if (res.status !== 200) {
    console.error('  SKIP: remaining Grafana datasource checks (non-200 response)');
    failed += 3; // prometheus, loki, jaeger
    return;
  }

  let datasources;
  try {
    datasources = await res.json();
  } catch (err) {
    assert(false, `Grafana /api/datasources response is valid JSON (parse error: ${err.message})`);
    failed += 3;
    return;
  }

  assert(Array.isArray(datasources), `Grafana datasources response is an array (got ${typeof datasources})`);

  const requiredTypes = [
    { label: 'Prometheus', type: 'prometheus' },
    { label: 'Loki', type: 'loki' },
    { label: 'Jaeger', type: 'jaeger' },
  ];

  const configuredTypes = Array.isArray(datasources)
    ? datasources.map(ds => (ds.type || '').toLowerCase())
    : [];

  for (const { label, type } of requiredTypes) {
    assert(configuredTypes.includes(type), `Grafana datasource "${label}" (type: ${type}) is configured`);
  }
}

async function testRedpandaMetrics() {
  console.log('\n--- Redpanda Metrics in Prometheus ---');

  const query = encodeURIComponent('redpanda_application_uptime_seconds_total');
  const res = await safeFetch(`${PROMETHEUS_URL}/api/v1/query?query=${query}`);
  assert(res.status === 200, `Prometheus instant query for redpanda_application_uptime_seconds_total returns 200 (got ${res.status})`);

  if (res.status !== 200) {
    console.error('  SKIP: redpanda metric result check (non-200 response)');
    failed++;
    return;
  }

  let data;
  try {
    data = await res.json();
  } catch (err) {
    assert(false, `Prometheus query response is valid JSON (parse error: ${err.message})`);
    return;
  }

  assert(data.status === 'success', `Prometheus query status is "success" (got "${data.status}")`);

  const results = (data.data && data.data.result) ? data.data.result : [];
  assert(results.length > 0, `redpanda_application_uptime_seconds_total metric has at least one result (confirms Redpanda scrape is active)`);
}

async function main() {
  console.log('\nTelemetry Health Tests\n');

  await testHealthEndpoints();
  await testPrometheusTargets();
  await testGrafanaDashboards();
  await testGrafanaDatasources();
  await testRedpandaMetrics();

  console.log(`\nResults: ${passed} passed, ${failed} failed\n`);
  process.exit(failed > 0 ? 1 : 0);
}

main().catch(err => {
  console.error('Fatal error:', err);
  process.exit(1);
});
