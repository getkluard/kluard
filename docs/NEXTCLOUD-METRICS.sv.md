# Nextcloud-mått, paneler och larm

Vad Kluard mäter på en Nextcloud-instans, var du ser det, hur du bygger paneler som byter färg och vilka larm du bör sätta upp först. Felsökning finns i [TROUBLESHOOTING-NEXTCLOUD.sv.md](TROUBLESHOOTING-NEXTCLOUD.sv.md).

## Varifrån siffrorna kommer

Agenten på maskinen som kontrollerar instansen samlar in tre sorters data, och tabellen längre ned visar vilket mått som kräver vad:

- **status.php** är öppen för alla och kräver inget. Den ger tillgänglighet, svarstid och underhållsläge.
- **serverinfo** kräver serverinfo-token eller en administratörsinloggning. Den ger användare, filer, delningar, storlekar, PHP-inställningar och uppdateringar.
- **En administratörsinloggning med app-lösenord** ger bakgrundsjobben, WebDAV-inloggningskontrollen, lagrad data och konton. Skapa app-lösenordet med `occ user:auth-tokens:add admin` och ange användare och lösenord under Edit instance.
- **Certifikatet** läses bara på https-adresser.

## Var du ser måtten

- **Metrics** i menyn listar varje mått som har kommit in. Väljer du ett Nextcloud-mått visas vad det betyder och rätt enhet ställs in. Dela på etiketten `instance` för en linje per instans.
- **Instances**: klicka på en instans för dess egen sida med rutor och diagram för allt nedan. Rutorna blir gula och röda av sig själva.
- **Dashboards**: bygg egna paneler, eller utgå från den färdiga **Nextcloud health**.

## Bygg en panel som blir gul och röd

Stat-paneler (ett stort tal) kan byta färg med värdet:

- Öppna *Dashboards*, öppna en dashboard och klicka *Edit dashboard*, sedan *+ Add panel*.
- Sätt *Panel type* till *Stat* och välj måttet, till exempel `nextcloud_cron_age_seconds`.
- Sätt *Split by* till `instance` och *Unit* till *Duration*.
- Välj *Colour* och *at or above*, och skriv `15m` för gult och `1h` för rött.
- Spara panelen och sedan dashboarden med *Save*.

Tider tar `15m`, `2h` eller `14d`, och storlekar tar `500MB` eller `10GB`. För värden där lågt är dåligt, som inloggningskontrollen (1 = fungerar) eller dagar kvar på ett certifikat, välj *at or below*. Lämna båda fälten tomma för ingen färg. En ruta med flera instanser färgar varje tal för sig.

## Den färdiga dashboarden

Lägg till **Nextcloud health** bland startdashboarderna under *Dashboards*. Den har rutor som blir gula och röda och diagrammen bakom dem:

| Ruta | Gul / röd |
|---|---|
| Last background job | 15 minuter / 1 timme sedan den senast kördes |
| Background job errors | 1 / 10 |
| Login check | röd när den fallerar |
| Certificate valid for | 21 dagar / 7 dagar kvar |
| Nextcloud update, app updates | gul när en väntar (röd vid 10 appar) |
| Free data space | 20 GB / 5 GB kvar |
| Response time | 2 s / 5 s |
| Maintenance mode | gul medan det är på |

Ändra gränserna genom att redigera panelen. Dashboarden kräver admin-inloggningen på varje instans för de fem första raderna.

## Larm att sätta upp först

En panel visar bara. För att bli meddelad, lägg till regler under *Alerts, Rules, Add rule* med typen *metric*, gruppera på `instance` och sätt *If there is no data* till *Raise an alert*, så att du också får veta när agenten tystnar.

| Regel | Varför |
|---|---|
| `nextcloud_cron_age_seconds`, highest, > 900, i 5 min | Bakgrundsjobben har stannat. Notiser, städning och delning slutar tyst fungera. |
| `nextcloud_webdav_ok`, lowest, < 1, i 5 min | Det går inte längre att logga in, trots att sidan svarar. |
| `nextcloud_tls_cert_expiry_seconds`, lowest, < 1209600 | Certifikatet går ut inom 14 dagar. |
| `nextcloud_up`, lowest, < 1, i 2 min | Instansen svarar inte. |
| `nextcloud_cron_errors`, highest, > 0 | Nextcloud rapporterar fel i sina bakgrundsjobb. |
| `nextcloud_data_free_bytes`, lowest, < 5000000000 | Mindre än 5 GB kvar för datan. |
| `nextcloud_update_available`, highest, >= 1 | Det finns en nyare Nextcloud. Sätt betydelsen till låg. |

## Alla Nextcloud-mått

Alla mått har etiketten `instance` (namnet du gav instansen). Övriga etiketter står vid varje mått. Om ett mått inte syns i *Metrics* visar kolumnen *Kräver* vad som saknas.

### Tillgänglighet och status

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_up` | 1 när status.php svarar, 0 när den inte gör det. Tillgänglighetssignalen. | alltid (status.php) | instance |
| `nextcloud_status_response_seconds` | Hur lång tid status.php tog på sig att svara. | alltid (status.php) | instance |
| `nextcloud_maintenance_mode` | 1 medan instansen står i underhållsläge. | alltid (status.php) | instance |
| `nextcloud_needs_db_upgrade` | 1 när Nextcloud väntar på en databasuppgradering (occ upgrade). | alltid (status.php) | instance |
| `nextcloud_installed` | 1 när Nextcloud rapporterar att den är installerad. | alltid (status.php) | instance |
| `nextcloud_info` | Alltid 1. Bär versionerna som etiketter, och det är vad sidan Instances visar. | kräver serverinfo-token eller admin-inloggning | instance, version, php_version, db_type, db_version, update_version |

### Användning och storlek

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_users` | Antal konton. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_files` | Antal filer. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_active_users` | Användare som varit aktiva de senaste 5 minuterna, timmen och 24 timmarna. | kräver serverinfo-token eller admin-inloggning | instance, period (5m, 1h, 24h) |
| `nextcloud_storages` | Antal lagringar per sort. | kräver serverinfo-token eller admin-inloggning | instance, type (total, local, home, other) |
| `nextcloud_shares` | Antal delningar per sort, inklusive federerade delningar skickade och mottagna. | kräver serverinfo-token eller admin-inloggning | instance, type (total, user, group, link, link_no_password, federated_sent, federated_received) |
| `nextcloud_data_free_bytes` | Ledigt utrymme där datakatalogen ligger. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_database_size_bytes` | Databasens storlek. | kräver serverinfo-token eller admin-inloggning | instance |

### Uppdateringar

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_apps_installed` | Antal installerade appar. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_apps_updates_available` | Antal appar som har en uppdatering som väntar. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_update_available` | 1 när det finns en nyare Nextcloud-version. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_update_checked_timestamp_seconds` | När Nextcloud senast sökte efter uppdateringar (Unix-tid). | kräver serverinfo-token eller admin-inloggning | instance |

### PHP

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_php_memory_limit_bytes` | PHP:s memory_limit. -1 betyder obegränsat och visas som ∞. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_php_max_execution_time_seconds` | PHP:s max_execution_time. 0 betyder obegränsat och visas som ∞. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_php_upload_max_filesize_bytes` | PHP:s upload_max_filesize, den största filen en uppladdning får innehålla. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_php_opcache_hit_rate` | Andel av PHP-anropen som serveras ur OPcache, i procent. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_php_opcache_used_bytes` | Minne som OPcache använder. | kräver serverinfo-token eller admin-inloggning | instance |
| `nextcloud_php_opcache_wasted_percent` | Andel av OPcache som är bortkastad. Ett högt värde betyder att den bör nollställas eller göras större. | kräver serverinfo-token eller admin-inloggning | instance |

### Bakgrundsjobb och inloggningskontroll

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_cron_age_seconds` | Sekunder sedan bakgrundsjobben senast kördes. Mycket stort om cron aldrig har kört. Det du ska larma på. | kräver admin-inloggning med app-lösenord | instance |
| `nextcloud_cron_last_run_timestamp_seconds` | När bakgrundsjobben senast kördes (Unix-tid). | kräver admin-inloggning med app-lösenord | instance |
| `nextcloud_cron_errors` | Antal cron-fel som Nextcloud rapporterar. | kräver admin-inloggning med app-lösenord | instance |
| `nextcloud_cron_mode` | Alltid 1. Läget står i etiketten. | kräver admin-inloggning med app-lösenord | instance, mode (cron, webcron, ajax) |
| `nextcloud_webdav_ok` | 1 när inloggning via WebDAV fungerar, 0 när den inte gör det. | kräver admin-inloggning med app-lösenord | instance |
| `nextcloud_webdav_response_seconds` | Hur lång tid WebDAV-inloggningen tog (bara medan den fungerar). | kräver admin-inloggning med app-lösenord | instance |

### Konton och lagrad data

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_storage_used_bytes` | Vad alla kontons filer tar tillsammans, så som Nextcloud räknar mot kvoten. Det fakturering utgår från. | kräver admin-inloggning med app-lösenord | instance |
| `nextcloud_accounts` | Antal aktiverade och avaktiverade konton. | kräver admin-inloggning med app-lösenord | instance, state (enabled, disabled) |

### Certifikat

| Mått | Vad det är | Kräver | Etiketter |
|---|---|---|---|
| `nextcloud_tls_cert_expiry_seconds` | Sekunder tills TLS-certifikatet går ut. Negativt när det har gått ut. | https-instanser (läser certifikatet) | instance |
| `nextcloud_tls_cert_not_after_timestamp_seconds` | När certifikatet går ut (Unix-tid). | https-instanser (läser certifikatet) | instance |
| `nextcloud_tls_cert_valid` | 1 när certifikatet är betrott och utfärdat för det här namnet. | https-instanser (läser certifikatet) | instance |

## Bra att veta

- Måtten för bakgrundsjobb och inloggning läses ungefär en gång i minuten och certifikatet var tionde minut, så räkna med en minut eller två efter en ändring.
- Om Nextcloud nekar inloggningen pausar agenten och måtten slutar komma. Larm med *Raise an alert* vid saknad data berättar det. Se felsökningsguiden.
- `nextcloud_cron_age_seconds` är mycket stort när cron aldrig har kört. Det är avsiktligt, så att larmet går.
- Obegränsad PHP-minnesgräns (-1) och körtid (0) behålls som de är och visas som ∞.

