# PromQL and Grafana

Kluard answers the Prometheus HTTP API under `/prom`, with a subset of PromQL. Point Grafana (or `curl`, or any Prometheus client) at it and write queries against the metrics Kluard has received.

It is read-only. Data still arrives through the agents and OTLP, never through Prometheus remote write.

## Connect Grafana

1. In Kluard open **Add a machine** and create a **read key** ("Read key for Grafana"). Copy it; it is shown once. A read key can read metrics and nothing else: it cannot send data, cannot sign in and cannot see traces, logs or settings. Delete it on the same page to revoke it.
2. In Grafana: **Connections → Data sources → Add → Prometheus**.
   - **URL:** `https://your-kluard-server/prom`
   - **Authentication:** *Basic authentication*. User: anything (for example `grafana`). Password: the read key. Or leave authentication off and add the custom header `Authorization` = `Bearer <read key>`.
   - **Prometheus type:** Prometheus. Scrape interval: the interval of your agents (15s is the default).
3. **Save & test.** It should say that the data source is working.

Each key belongs to one tenant, so a data source only ever sees the data of its own tenant.

Try it from a shell:

```sh
curl -s -H "Authorization: Bearer klr_..." 'https://your-kluard-server/prom/api/v1/query?query=sum(nextcloud_users)'
```

## Names

Prometheus allows only letters, digits, `_` and `:` in names, so everything else becomes `_`:

| In Kluard | In PromQL |
|---|---|
| `system.load.5m` | `system_load_5m` |
| `nextcloud_cron_age_seconds` | the same |
| label `host.name` | `host_name` |

Every series also has the label `service` (the service that sent it) and the labels the agent attached (`host`, `device`, `container`, …). The Metrics page and the [Nextcloud metrics list](NEXTCLOUD-METRICS.md) show what exists.

## What is supported

**Selectors:** `metric`, `metric{label="x", other!="y", name=~"a.*", name!~"b.*"}`, `{__name__=~"system_.*"}`, `offset` (`metric offset 5m`), range selectors `metric[5m]`. Regular expressions are anchored, as in Prometheus. Instant selectors look back 5 minutes for a sample.

**Functions:** `rate`, `irate`, `increase`, `delta`, `idelta`, `changes`, `resets`, `avg_over_time`, `sum_over_time`, `min_over_time`, `max_over_time`, `count_over_time`, `last_over_time`, `present_over_time`, `stddev_over_time`, `stdvar_over_time`, `quantile_over_time`, `abs`, `ceil`, `floor`, `round`, `sqrt`, `exp`, `ln`, `log2`, `log10`, `sgn`, `clamp`, `clamp_min`, `clamp_max`, `time`, `vector`, `scalar`, `absent`, `sort`, `sort_desc`.

**Aggregations:** `sum`, `avg`, `min`, `max`, `count`, `group`, `stddev`, `stdvar`, `quantile`, `topk`, `bottomk`, with `by (...)` or `without (...)`, before or after the expression.

**Operators:** `+ - * / % ^`, comparisons `== != > < >= <=` (also with `bool`), `and`, `or`, `unless`, with `on (...)` and `ignoring (...)`, and unary minus.

**API endpoints** (GET, and POST for the long ones): `/prom/api/v1/query`, `query_range`, `series`, `labels`, `label/<name>/values`, `metadata`, `status/buildinfo`.

Examples:

```promql
# a background job has not run for 15 minutes
nextcloud_cron_age_seconds > 900

# fullest disks first
sort_desc(system_disk_utilization)

# requests per second, per host
sum by (host) (rate(http_requests_total[5m]))

# average 5-minute load over the last hour
avg_over_time(system_load_5m[1h])
```

## What is not supported

- Subqueries (`metric[5m:1m]`), the `@` modifier, `group_left` / `group_right` (many-to-one matching) and native histograms. `histogram_quantile`, `label_replace`, `label_join`, `deriv`, `predict_linear` and the date functions are not there yet. Using one gives a clear error, never a wrong answer.
- Recording rules and the Prometheus alerting API. Alerts are set up in Kluard under **Alerts**.
- Remote write. Send data with the Kluard agent or OTLP.

## Limits

- A range query may span at most 90 days and 11,000 points per series (pick a larger step if Grafana asks for too many).
- One selector may match at most 50 metrics (`{__name__=~...}`) and one query may load at most 20,000 series and 500,000 samples per metric. Narrow the selector or the range if you hit it; the error says which limit.
- `labels` and `label/<name>/values` without `match[]` look at a sample of the metrics (the first 30). Grafana's variable queries (`label_values(metric, label)`) pass `match[]` and are complete.
- Kluard stores raw points and computes in the server, so very wide queries are slower than in Prometheus. Use the dashboards in Kluard for everyday views and Grafana when you need its panels or already have dashboards.
