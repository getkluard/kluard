# Felsökning av Nextcloud-övervakning

Se även [Nextcloud-mått, paneler och larm](NEXTCLOUD-METRICS.sv.md) för varje mått och de larm du bör sätta upp.

Hitta meddelandet du ser i Kluard, läs vad det betyder och kör kontrollen. Allt nedan har setts på riktiga installationer.

## Innan du börjar

**Vilken maskin gör kontrollen?** Under instansen står "checked by" med ett maskinnamn. Agenten på **den** maskinen når Nextcloud, inte Kluards server och inte din dator. Kör varje `curl` nedan **på maskinen som står i "checked by"**. Ett test från en annan maskin kan ge `HTTP 000` medan agenten fungerar, eller tvärtom.

**Var står orsaken?** Sidan säger "the reason is in the agent's log". Läs den på "checked by"-maskinen:

```bash
systemctl list-units --type=service | grep -i -E "kluard|lumen"
journalctl -u TJÄNSTENS-NAMN -n 30 --no-pager | cut -c1-350
```

Leta efter `nextcloud poll problem`. Texten efter `err` är den exakta orsaken.

**Köra `occ`.** Hur du kör det beror på hur Nextcloud är installerat. Byt `NC` mot containerns namn (`docker ps` visar det):

| Installation | Kommando |
|---|---|
| Docker | `docker exec -u www-data NC php occ ...` |
| Nextcloud AIO | `docker exec --user www-data -it nextcloud-aio-nextcloud php occ ...` |
| Paket eller manuell | `sudo -u www-data php /var/www/nextcloud/occ ...` (webbanvändaren kan heta `apache` eller `nginx`) |
| Snap | `sudo nextcloud.occ ...` |

**Testa token som agenten gör:**

```bash
TOKEN='DIN_TOKEN'
curl -s -o /tmp/si.txt -w 'HTTP %{http_code}\n' -H "Accept: application/json" -H "OCS-APIRequest: true" -H "NC-Token: $TOKEN" "http://ADRESS/ocs/v2.php/apps/serverinfo/api/v1/info?format=json"
head -c 300 /tmp/si.txt; echo
```

`HTTP 200` och JSON som börjar med `{"ocs":{"meta":{"status":"ok"` betyder att adress och token är rätt.

## Instansen är nere, eller "Waiting for the first check"

| Det du ser | Orsak och åtgärd |
|---|---|
| `invalid nextcloud url` | Adressen är ingen fullständig URL. Skriv den som `https://moln.exempel.se` eller `http://192.168.1.10:8080`, med protokoll. |
| `status.php returned HTTP 404 ... the URL must be the base address` | Adressen har en sökväg (`/login`, `/index.php`; `/nextcloud` är bara rätt om Nextcloud verkligen ligger där). Använd basadressen. Test: `curl -s http://ADRESS/status.php` ska skriva ut JSON. |
| `status.php did not return JSON (is this a Nextcloud URL?)` | Något annat svarar: en inloggningssida, en proxys felsida, en omdirigering. Kolla med `curl -sL -o /dev/null -w '%{http_code} %{url_effective}\n' http://ADRESS/status.php`. En omdirigering från http till https betyder att du ska använda https-adressen. |
| Timeout, `HTTP 000`, anslutning vägras | Agentens maskin når inte adressen. Kontrollera adress, port, brandvägg och att Nextcloud körs (`docker ps`). Kom ihåg att testet ska köras på "checked by"-maskinen. |
| `x509: certificate ...` på en https-adress | Agentens maskin litar inte på certifikatet (självsignerat eller intern utfärdare). Installera utfärdaren på maskinen, eller övervaka via den interna http-adressen. |
| `HTTP 400` eller "Access through untrusted domain" | Adressen finns inte i `trusted_domains`. Lista dem med `occ config:system:get trusted_domains` och lägg till en: `occ config:system:set trusted_domains 3 --value="192.168.1.10:8080"` (nästa lediga nummer). |
| `HTTP 502`, `503`, `504` | Proxyn framför Nextcloud når den inte, eller Nextcloud är överbelastad eller startar om. Titta i proxyns logg och `docker logs NC`. |
| Nere bara korta stunder | Underhållsläge (`nextcloud_maintenance_mode` = 1), en omstart, eller en backup som stoppar containrarna. Loggen visar när. |

## "Only availability is monitored"

Agenten når Nextcloud men serverinfo-anropet misslyckas, så användare, filer, delningar, lagring och PHP saknas. Agentens logg visar HTTP-koden:

| `serverinfo returned HTTP ...` | Orsak och åtgärd |
|---|---|
| `401` eller `403` | Nextcloud godtar inte token. Sätt den igen: `occ app:enable serverinfo`, `occ config:app:set serverinfo token --value "TOKEN"`, och läs tillbaka den med `occ config:app:get serverinfo token`. Klistra in samma värde i Kluard (**Edit**). Skriv inget för hand: en radbrytning mitt i en inklistrad token gör att den inte fungerar. |
| `404` | Appen serverinfo är inte aktiverad: `occ app:enable serverinfo`. |
| `400` | Obetrodd domän, se ovan. |
| `429` ("Reached maximum delay") | Nextclouds brute force-skydd har blockerat agentens adress, oftast efter nekade inloggningar (fel lösenord i inloggningsfälten, eller en gammal token). Se "Blockerad av brute force-skyddet" nedan. |
| `500` | Ett fel inne i Nextcloud. Läs dess logg (se "En enorm loggrad" om `occ log:tail` kraschar). Om det kommer efter ett `429` är det samma blockering. |
| `serverinfo did not return JSON` | En proxy eller inloggningssida svarade. Kontrollera adressen och att proxyn släpper igenom huvudena `NC-Token` och `OCS-APIRequest`. |
| `serverinfo is paused until HH:MM` | Agenten slutade fråga efter ett nekat anrop (för att inte förvärra blockeringen). Åtgärda orsaken, öppna sedan **Edit** på instansen och tryck **Save** en gång. Då upphör pausen. |

Andra orsaker:

- Du klistrade in token men sparade inte, eller sparade den på en annan instans.
- Både token och inloggning är ifyllda, och lösenordet till inloggningen är fel. Token används för serverinfo, men inloggningen används för de djupare kontrollerna och ett fel lösenord blockerar adressen (se de två följande avsnitten). Ta bort inloggningen, eller ge den ett korrekt app-lösenord.
- Agenten är äldre än Kluard. Sidan säger det då; uppdatera agenten.

## "Background jobs and the login check cannot be read"

De här kontrollerna kräver ett Nextcloud-konto som är **administratör**. Ge instansen ett användarnamn och ett **app-lösenord** (inte det vanliga lösenordet). Agentens logg visar vilket av dessa det är:

| Meddelande | Åtgärd |
|---|---|
| Ingen inloggning angiven | Öppna **Edit**, fyll i "or login": användarnamn och app-lösenord. Bocka inte "remove". |
| `the user name or app password was refused (HTTP 401)` | Fel användare eller lösenord, eller så har app-lösenordet tagits bort. Skapa ett nytt. |
| `the user must be a Nextcloud administrator ... (HTTP 403)` | Användaren ingår inte i gruppen `admin`. Använd en administratör. |
| `the Provisioning API is not available ... enable the Provisioning API app` | `occ app:enable provisioning_api`. |
| Tvåfaktorsautentisering är på för användaren | Det vanliga lösenordet nekas. Ett app-lösenord fungerar ändå. |
| `paused until HH:MM ... (brute-force protection)` | Se "Blockerad av brute force-skyddet". |

Skapa ett app-lösenord från kommandoraden (det visas bara en gång):

```bash
occ user:auth-tokens:add admin
```

Eller i Nextcloud: **Inställningar, Säkerhet, Enheter och sessioner, Skapa nytt app-lösenord**.

## "The login check fails"

Agenten loggar in över WebDAV och listar användarens filer. Det går genom webbservern, PHP, databasen och lagringen, så det hittar fel som `status.php` inte gör.

| Meddelande | Orsak och åtgärd |
|---|---|
| `the WebDAV login was refused (HTTP 401)` | Fel användare eller app-lösenord. |
| `the WebDAV check returned HTTP 5xx` | Nextcloud fallerar internt: full disk, databas som ligger nere, lagring som saknas (en omonterad datakatalog, en S3-bucket som inte svarar). Läs Nextclouds logg och `docker logs NC`. |
| `HTTP 403` eller `423` | Användaren är låst, eller instansen är i underhållsläge. |
| `HTTP 404` | Användaren har ingen hemkatalog än. Logga in som användaren en gång, eller använd en annan. |

## "Background jobs have not run for ..."

Nextclouds egna jobb (städning, förhandsvisningar, notiser, e-post) körs av cron. Sidan visar tiden sedan senaste körning.

- Cron är inte uppsatt. Kör det var 5:e minut: `*/5 * * * * www-data php -f /var/www/nextcloud/cron.php`. För Docker kör du från värden: `*/5 * * * * docker exec -u www-data NC php -f /var/www/html/cron.php`.
- Läget säger "Cron" men inget kör det. Kolla under **Administrationsinställningar, Grundinställningar**, och cron-tjänsten eller containern.
- Läget "AJAX" eller "Webcron" körs bara när någon använder webbplatsen. Byt till Cron: `occ background:cron`.
- Ett jobb har fastnat. Leta efter en långkörande `cron.php`-process och efter ett lås i Redis (en Redis som ligger nere gör sidor och jobb långsamma).
- "As long as anyone can tell" betyder att cron aldrig har körts på instansen.

## Blockerad av brute force-skyddet

Nextcloud fördröjer eller blockerar en adress efter upprepade nekade inloggningar. Agentens adress får då `HTTP 429` ("Reached maximum delay") eller märkliga `500`-svar, och alla kontroller som kräver inloggning stannar. Vanliga orsaker: fel lösenord i inloggningsfälten, en gammal token, ett testskript med fel lösenord.

1. Ta bort orsaken först (rätta eller ta bort inloggningen i Kluard), annars händer det igen.
2. Nollställ adressen så som Nextcloud ser den. För Docker är det ofta Docker-gatewayn (till exempel `172.17.0.1`), och för ett lokalt test maskinens egen adress:

   ```bash
   occ security:bruteforce:reset 192.168.1.10
   occ security:bruteforce:reset 172.17.0.1
   ```

3. Lägg övervakningsnätet på vitlistan så att det aldrig blockeras:

   ```bash
   occ config:app:set bruteForce whitelist_0 --value="192.168.1.0/24"
   occ config:app:set bruteForce whitelist_1 --value="172.16.0.0/12"
   ```

4. Testa med `curl` ovan (den ska ge `HTTP 200`), öppna sedan **Edit** på instansen och tryck **Save** en gång så att agenten fortsätter.

## Certifikat

| Det du ser | Betydelse |
|---|---|
| "The certificate is not checked because the address is not https" | Normalt för en intern http-adress. Certifikatet som besökarna ser ligger på den publika adressen. Lägg till den publika adressen som en egen instans om du vill kontrollera det. |
| "The certificate expires in ..." | Mindre än 14 dagar kvar. Förnyas det automatiskt är förnyelsen trasig: titta i loggen för ACME-klienten eller proxyn. |
| "The certificate expired ... ago" | Webbläsare vägrar sidan. Förnya det nu. |
| "The certificate is not trusted, or not for this name" | Ett självsignerat certifikat, ett saknat mellancertifikat eller ett annat namn. Okej för en intern instans med egen utfärdare, men ett riktigt problem på en publik. |

## Data saknas ("No data"-rutor)

- Användare, filer, delningar, lagring, databasstorlek och PHP-rutorna kommer från serverinfo och kräver token. "Only availability is monitored" förklarar varför de är tomma.
- Federerade delningar (skickade och mottagna) räknar det serverinfo rapporterar. De är 0 när federationsapparna inte används.
- **PHP memory limit** visar ett oändlighetstecken när PHP har `memory_limit = -1`. **PHP max run time** visar det när värdet är 0.
- **PHP upload limit** kräver en agent av samma version som Kluard eller nyare.
- **OPcache**-rutorna är tomma när OPcache är av (`opcache.enable=0`) eller inte går att läsa.
- Sista bakgrundsjobb, inloggningstest, appar och uppdateringar kräver administratörsinloggningen (se ovan).
- "Free data space" är tom på lagringar som inte rapporterar ledigt utrymme (en del S3- och nätverksmonteringar).
- Kontrollera tidsintervallet högst upp på sidan. En ny instans har ingen historik före sin första rapport.

## Agenten

| Det du ser | Åtgärd |
|---|---|
| "last report" är gammal, eller värden saknas | Agenten är stoppad eller når inte Kluard. På maskinen: `systemctl status NAMN`, sedan `journalctl -u NAMN -n 30`. Kontrollera att maskinen når `url` i `/etc/kluard-agent/config.json` (port 4318 som standard, eller din https-adress): `curl -s -o /dev/null -w '%{http_code}\n' http://KLUARD:4318/healthz` ska skriva `200`. |
| `kluard rejected /v1/metrics: 401 Unauthorized` | Servern känner inte igen agentens API-nyckel. Kluard listar sådana agenter på sidan **Hosts** ("An agent is being refused", med nyckelns första 8 tecken och varifrån den kom) och skriver `agent key refused` i sin egen logg. Lägg en giltig nyckel i `/etc/kluard-agent/config.json` och starta om agenten. Nycklar skapas under **Add a machine**; nycklarna i `KLUARD_API_KEYS` (eller gamla `LUMEN_API_KEYS`) i `deploy/.env` fungerar alltid. |
| "The agent that checks this instance is older than Kluard" | Uppdatera agenten till Kluards version. Nyare funktioner kräver det. |
| `This machine refused ... log paths` | Av säkerhetsskäl läser en agent bara filer under `/var/log`, `/var/lib/docker`, `/var/www`, `/srv`, `/mnt` och `/opt`. Ändra `allowed_log_dirs` i `/etc/kluard-agent/config.json`, eller flytta filen. |
| Loggfilen visar ingenting | Kontrollera sökvägen, att agenten kan läsa filen (`ls -l`) och att agenten startades om efter ändringen. |

## En enorm loggrad

`occ log:tail` kan stanna med `Allowed memory size ... exhausted` när en loggpost är enorm (ofta ett långt undantag från ett misslyckat HTTP-anrop). Läs den råa filen med långa rader avkortade i stället:

```bash
docker exec NC sh -c 'tail -n 20 /var/www/html/data/nextcloud.log' | cut -c1-300
```

Finns inte filen där, ta reda på var den ligger: `occ config:system:get logfile` och `occ config:system:get log_type`. En så stor loggpost är värd att undersöka, eftersom samma anrop misslyckas om och om igen.

## Misstag som ser ut som fel

- Testet kördes på fel maskin (`HTTP 000`).
- Ett kommando kopierades med platshållaren kvar (`NC`, `DIN_TOKEN`, `ADRESS`).
- Ett helt svar, med förklaringarna, klistrades in i skalet.
- Ett mellanslag eller en radbrytning i token.
- Vanligt lösenord användes i stället för app-lösenord.
- Containernamnet `NC` användes i stället för det riktiga.
- Gamla resultat lästes: hårduppdatera sidan (Ctrl+Shift+R) efter att du sparat, och vänta en till två minuter. Kontrollerna körs var 15:e sekund, de djupare mer sällan.

## Kommer du inte vidare?

Samla de här tre sakerna och skicka dem med din fråga:

1. Agentens loggrader med `nextcloud poll problem`.
2. Resultatet av `curl`-testet ovan (HTTP-koden och de första 300 tecknen).
3. Nextcloud-versionen och hur den är installerad (Docker, AIO, paket).
