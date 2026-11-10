# PromQL och Grafana

Kluard svarar på Prometheus HTTP-API under `/prom`, med en delmängd av PromQL. Peka Grafana (eller `curl`, eller valfri Prometheus-klient) mot den och skriv frågor mot de mätvärden Kluard har tagit emot.

Det är skrivskyddat. Data kommer fortfarande in via agenterna och OTLP, aldrig via Prometheus remote write.

## Koppla Grafana

1. Öppna **Add a machine** i Kluard och skapa en **läs-nyckel** ("Read key for Grafana"). Kopiera den; den visas en gång. En läs-nyckel kan läsa mätvärden och inget annat: den kan inte skicka data, inte logga in och inte se spår, loggar eller inställningar. Ta bort den på samma sida för att dra tillbaka den.
2. I Grafana: **Connections → Data sources → Add → Prometheus**.
   - **URL:** `https://din-kluard-server/prom`
   - **Authentication:** *Basic authentication*. Användare: valfri (till exempel `grafana`). Lösenord: läs-nyckeln. Eller lämna autentisering av och lägg till headern `Authorization` = `Bearer <läs-nyckel>`.
   - **Prometheus type:** Prometheus. Scrape interval: agenternas intervall (15 s är standard).
3. **Save & test.** Det ska stå att datakällan fungerar.

Varje nyckel hör till en tenant, så en datakälla ser bara sin egen tenants data.

Prova från ett skal:

```sh
curl -s -H "Authorization: Bearer klr_..." 'https://din-kluard-server/prom/api/v1/query?query=sum(nextcloud_users)'
```

## Namn

Prometheus tillåter bara bokstäver, siffror, `_` och `:` i namn, så allt annat blir `_`:

| I Kluard | I PromQL |
|---|---|
| `system.load.5m` | `system_load_5m` |
| `nextcloud_cron_age_seconds` | samma |
| etikett `host.name` | `host_name` |

Varje serie har också etiketten `service` (tjänsten som skickade den) och de etiketter agenten satte (`host`, `device`, `container`, …). Sidan Metrics och [listan över Nextcloud-mätvärden](NEXTCLOUD-METRICS.sv.md) visar vad som finns.

## Vad som stöds

**Väljare:** `metric`, `metric{label="x", annan!="y", namn=~"a.*", namn!~"b.*"}`, `{__name__=~"system_.*"}`, `offset` (`metric offset 5m`), intervallväljare `metric[5m]`. Reguljära uttryck är förankrade, som i Prometheus. Vanliga väljare letar 5 minuter bakåt efter ett värde.

**Funktioner:** `rate`, `irate`, `increase`, `delta`, `idelta`, `changes`, `resets`, `avg_over_time`, `sum_over_time`, `min_over_time`, `max_over_time`, `count_over_time`, `last_over_time`, `present_over_time`, `stddev_over_time`, `stdvar_over_time`, `quantile_over_time`, `abs`, `ceil`, `floor`, `round`, `sqrt`, `exp`, `ln`, `log2`, `log10`, `sgn`, `clamp`, `clamp_min`, `clamp_max`, `time`, `vector`, `scalar`, `absent`, `sort`, `sort_desc`.

**Aggregeringar:** `sum`, `avg`, `min`, `max`, `count`, `group`, `stddev`, `stdvar`, `quantile`, `topk`, `bottomk`, med `by (...)` eller `without (...)`, före eller efter uttrycket.

**Operatorer:** `+ - * / % ^`, jämförelser `== != > < >= <=` (även med `bool`), `and`, `or`, `unless`, med `on (...)` och `ignoring (...)`, samt unärt minus.

**API-endpoints** (GET, och POST för de långa): `/prom/api/v1/query`, `query_range`, `series`, `labels`, `label/<namn>/values`, `metadata`, `status/buildinfo`.

Exempel:

```promql
# ett bakgrundsjobb har inte körts på 15 minuter
nextcloud_cron_age_seconds > 900

# fullaste diskarna först
sort_desc(system_disk_utilization)

# förfrågningar per sekund, per host
sum by (host) (rate(http_requests_total[5m]))

# medelbelastning (5 min) senaste timmen
avg_over_time(system_load_5m[1h])
```

## Vad som inte stöds

- Subqueries (`metric[5m:1m]`), `@`-modifieraren, `group_left` / `group_right` (många-till-en) och inbyggda histogram. `histogram_quantile`, `label_replace`, `label_join`, `deriv`, `predict_linear` och datumfunktionerna finns inte än. Då får du ett tydligt fel, aldrig ett felaktigt svar.
- Recording rules och Prometheus larm-API. Larm ställs in i Kluard under **Alerts**.
- Remote write. Skicka data med Kluard-agenten eller OTLP.

## Gränser

- En intervallfråga får spänna högst 90 dagar och 11 000 punkter per serie (välj större steg om Grafana ber om för många).
- En väljare får matcha högst 50 mätvärden (`{__name__=~...}`) och en fråga får läsa högst 20 000 serier och 500 000 värden per mätvärde. Smalna av väljaren eller tidsintervallet om du når gränsen; felet säger vilken.
- `labels` och `label/<namn>/values` utan `match[]` tittar på ett urval av mätvärdena (de 30 första). Grafanas variabelfrågor (`label_values(metric, label)`) skickar `match[]` och är kompletta.
- Kluard lagrar råa punkter och räknar i servern, så mycket breda frågor är långsammare än i Prometheus. Använd dashboards i Kluard för det dagliga och Grafana när du behöver dess paneler eller redan har dashboards.
