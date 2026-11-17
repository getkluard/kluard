# Säkerhet i Kluard: översikt

*English: [SECURITY-OVERVIEW.md](SECURITY-OVERVIEW.md). För att rapportera en sårbarhet, se [SECURITY.md](../SECURITY.md).*

Den här sidan beskriver vad Kluard gör för att skydda dina data, var gränserna går och vad du själv måste göra. Den är skriven utifrån koden i community-utgåvan (Apache-2.0), version 0.1.x. Enterprise-funktioner (`ee/`: SAML, SCIM, påtvingad MFA med mera, se [EDITIONS.sv.md](EDITIONS.sv.md)) nämns bara där de skiljer sig.

**Vad sidan inte är.** Kluard har inte genomgått någon oberoende säkerhetsgranskning eller penetrationstest. Allt nedan är vad projektets egen kod och egna tester gör. Läs det som en beskrivning att jämföra med dina egna krav, inte som ett certifikat.

## 1. Delarna och vad de innehåller

| Del | Vad den innehåller | Vem kommer åt den |
|---|---|---|
| **Kluard-servern** (ett Go-program som körs som icke-root i en "distroless"-container) | Visar webbgränssnittet och API:et på port 4318 | Alla som når port 4318: sätt HTTPS framför |
| **ClickHouse** | Telemetri: spår, loggar och mätvärden, per tenant | Bara Kluard-servern, på det interna Docker-nätverket. Port 8123 är inte publicerad. Lösenordsskyddad |
| **Elasticsearch** | Användare, grupper, hashar av API-nycklar, dashboards, värdar, instanser (med krypterade inloggningsuppgifter), sessionshemligheten och granskningsloggen | Bara Kluard-servern, på det interna Docker-nätverket. Port 9200 är inte publicerad. Lösenordsskyddad (`xpack.security`), vanlig HTTP inom det nätverket |
| **Agenter** (på maskinerna du övervakar) | En konfigurationsfil med API-nyckeln och, om du använder dem, Nextcloud-uppgifter | De ansluter **utåt** till servern. Ingenting ansluter in till en agent om du inte slår på dess metrikport |
| **Backupmappen** | Dagliga exporter av telemetri och en konfigurationsdump | Serverns disk. Bara den som har åtkomst till servern kan läsa den |

Installation från release-fil (väg B i [INSTALL.md](../INSTALL.md)) kör samma delar: servern under systemd och ClickHouse i en Docker-container som bara lyssnar på `127.0.0.1`.

## 2. Inloggning

- **Personer** loggar in med användarnamn och lösenord. Lösenord lagras som saltad PBKDF2-HMAC-SHA256 med 600 000 iterationer och jämförs i konstant tid.
- **Sessioner** är signerade cookies: `HttpOnly`, `SameSite=Strict` och `Secure` när anropet kom över HTTPS (även bakom en proxy som skickar `X-Forwarded-Proto: https`). En session varar i 12 timmar. När ett lösenord byts avslutas den användarens sessioner.
- **Begränsning av inloggningsförsök.** Efter 10 misslyckade försök inom 10 minuter spärras ett användarnamn en stund. Inloggning med API-nyckel begränsas per klientadress. Räknarna ligger i serverns minne, så en omstart nollställer dem.
- **Anrop från andra webbplatser.** API:et tar bara emot JSON (`Content-Type: application/json`), vilket en webbläsare inte kan skicka från en annan webbplats utan tillstånd. Tillsammans med `SameSite=Strict`-cookies stoppar det förfalskade anrop mellan webbplatser (CSRF). Kroppar har en storleksgräns.
- **Maskiner** använder **API-nycklar**. Nycklar lagras bara som SHA-256-hashar och visas en gång. En webbläsarsession kan aldrig skicka telemetri. En person kan också logga in med en API-nyckel. Den sessionen kan inte hantera nycklar, användare eller lösenord och upphör när nyckeln raderas.
- **Ingår inte i community-utgåvan:** inbyggd flerfaktorsinloggning (MFA). Påtvingad MFA, SAML och SCIM är Enterprise-funktioner. Behöver du MFA i community-utgåvan, sätt en autentiserande proxy (till exempel en som sköter SSO) framför Kluard.

## 3. Vem ser vad

- **Tenants.** Varje fråga mot telemetri filtreras på den inloggades tenant, i servern, och tenanten skickas som en frågeparameter och klistras inte in i SQL-texten. Ett dolt eller ändrat gränssnitt ändrar inte vad API:et tillåter.
- **Behörigheter.** Användare tillhör grupper och varje grupp har behörigheter (till exempel Alerts, Notification channels, Keys, Users). Servern kontrollerar dem vid varje anrop. Den inbyggda gruppen *User* kan läsa larm men inte se notifieringskanaler, eftersom kanaler innehåller hemligheter.
- **Granskningslogg.** Ändringar loggas (vem, vad, från vilken adress och hur personen loggade in). I installationer med flera tenants kan en tenants administratörer läsa posterna om sin egen tenant, inklusive operatörens supportåtkomst.

## 4. Hemligheter och kryptering

- **Lagrade inloggningsuppgifter** (Nextcloud-token, appnycklar, hemligheter i kanaler) krypteras med AES-256-GCM innan de sparas, med nyckeln härledd från `KLUARD_SECRET_KEY`. De returneras aldrig av API:et.
- **`KLUARD_SECRET_KEY` måste vara slumpmässig och sparas.** Installationsprogrammet skapar en slumpmässig nyckel. Nyckeln härleds med en enda SHA-256, vilket är tillräckligt för en slumpmässig nyckel men svagt för ett valt ord. Utan nyckeln går de lagrade uppgifterna inte att läsa igen. Om den saknas faller Kluard tillbaka på sessionshemligheten och loggar en varning.
- **`deploy/.env`** innehåller nyckeln, databaslösenorden och första administratörens lösenord. Ha den `chmod 600`, säkerhetskopiera den utanför servern och checka aldrig in den.
- **Användarnas lösenord** lagras aldrig i läsbar form (se avsnitt 2). API-nycklar likaså.
- **Kryptering under överföring.** Kluard själv talar vanlig HTTP på port 4318. API-nycklar och agentkonfiguration (som innehåller Nextcloud-uppgifter för de kontroller agenten gör) skickas över den anslutningen, så **använd HTTPS**: Nginx eller Caddy framför, och `KLUARD_BIND=127.0.0.1` så att den okrypterade porten inte är öppen mot nätverket. Servern sätter inte HSTS själv; gör det i proxyn. Trafiken mellan Kluard och databaserna är vanlig HTTP på det interna Docker-nätverket.
- **Data i vila** krypteras inte av Kluard. Använd krypterad disk eller volym om du behöver det.

## 5. Agenten

- **Bara utgående.** Agenten ansluter till serverns adress med sin API-nyckel och skickar data. Den ansluter också till de tjänster du bett den kontrollera (Nextcloud, nginx, databaser, S3). Servern kan inte få en agent att köra kommandon: den kan bara ändra agentens inställningar och erbjuda en ny agentversion (se *Uppdateringar kontrolleras* nedan).
- **Vad den startar.** De enda program agenten själv kör är `systemctl` (för att lista tjänster) och, vid uppdatering, den nya agentfilen med `-version` för att se att den startar.
- **Fjärrinställningar är begränsade.** Servern kan tala om för en agent vilka loggfiler den ska läsa, men agenten vägrar sökvägar utanför en tillåtelselista (`/var/log`, Dockers datamappar, `/var/www`, `/srv`, `/mnt`, `/opt`), relativa sökvägar och `..`. Listan kan bara breddas i agentens egen konfigurationsfil, som ägs av root.
- **Installerad som tjänst** körs agenten som en egen användare (`kluard-agent`) med `NoNewPrivileges`, `ProtectSystem=strict` och `PrivateTmp`, och en tillagd förmåga: läsåtkomst till filer (`CAP_DAC_READ_SEARCH`), så att den kan läsa loggar som tillhör andra användare. Den kan inte skriva utanför sin egen tillståndsmapp. Konfigurationsfilen är `root:kluard-agent`, läge 640.
- **Docker.** `--docker` ger agenten åtkomst till Docker-socketen. **Det motsvarar root på den maskinen.** Använd det bara på maskiner där det är acceptabelt. Med rootless Docker (`--docker-rootless`) körs agenten som den användaren med användarens egen socket, vilket är mindre än root.
- **Uppdateringar kontrolleras.** Installationsprogrammet och självuppdateringen jämför den nedladdade agenten med de SHA-256-summor som *din* Kluard-server publicerar och vägrar vid avvikelse. Det skyddar mot en trasig nedladdning, och mot en ändrad fil så länge servern själv inte är komprometterad: en komprometterad Kluard-server kan erbjuda en skadlig agent, så skydda servern som ett hanteringssystem.
- **Windows-agenten:** inte granskad för den här sidan.

## 6. Vad servern ansluter till

Servern ansluter utåt bara till ClickHouse och Elasticsearch, och till de adresser som personer ställer in: larmkanaler (webhooks och mailservrar) och, i Enterprise-utgåvan, en identitetsleverantör (OIDC). Det finns ingen användningsrapportering eller "ringa hem", och licenser kontrolleras offline (signerade med Ed25519, ingen förfrågan till oss). Ingen annan utgående klient hittades i serverkoden.

Larmkanaler låter en användare få servern att ansluta till en adress de skriver in. För att det inte ska kunna användas för att nå ditt interna nätverk **vägrar Kluard loopback-, privata och länklokala adresser**, kontrollerat på den faktiska anslutningen (alltså även efter namnuppslag och omdirigeringar). Länklokala adresser (169.254.x.x, där molnleverantörer har instansens uppgifter) förblir blockerade även när en administratör tillåter privata adresser med `KLUARD_ALERT_ALLOW_PRIVATE=true`.

## 7. Webbgränssnittet

- Gränssnittet levereras med `Content-Security-Policy: default-src 'self'; img-src 'self' data:; ... frame-ancestors 'none'` och `X-Content-Type-Options: nosniff`. `frame-ancestors 'none'` hindrar att det visas i en ram på en annan webbplats.
- **En begränsning:** gränssnittet är en enda HTML-fil med skriptet inbäddat, så policyn måste tillåta inbäddade skript (`'unsafe-inline'`). Den begränsar var skript, stilar och anslutningar får komma ifrån, men det är inte den strängaste formen av policyn.
- Uppladdade logotyper kontrolleras utifrån sitt verkliga innehåll, har en storleksgräns, vägras om en SVG innehåller skript och levereras med `Content-Security-Policy: sandbox` och `nosniff`.
- Den publika statussidan (om du använder den) levereras med en egen innehållspolicy och `no-referrer`.

## 8. Backuper

Backuper innehåller **alla tenants data** och en konfigurationsdump med lösenordshashar och krypterade uppgifter. De är bara för operatören och serveras aldrig av API:et. Som standard ligger de på samma server som data: kopiera dem någon annanstans och skydda kopian som databasen. Lagringstiden ställs in i *Settings, How long data is kept*.

## 9. Att hålla Kluard uppdaterat

Kluard har inga Go-beroenden utöver Go:s standardbibliotek, så leveranskedjan är kort. En veckovis automatisk kontroll av kända sårbarheter körs på Go-verktygen, och förslag på uppdateringar av GitHub Actions och basavbilder görs varje vecka. Se [SECURITY.md](../SECURITY.md) för schemat och för hur du rapporterar ett problem. Databaserna körs på versioner som tillverkarna fortfarande uppdaterar (Elasticsearch 8.19, ClickHouse 26.8; se [UPGRADING.md](../UPGRADING.md)), och att byta dem är ett beslut att ta och testa på en kopia.

## 10. Vad du själv måste göra

1. Kör Kluard över **HTTPS** och håll port 4318 stängd mot nätverket (`KLUARD_BIND=127.0.0.1`) när en proxy står framför. Portar som Docker publicerar går ofta förbi firewalld och ufw, så lita inte bara på brandväggen.
2. Håll `deploy/.env` privat och säkerhetskopierad. Förlora aldrig `KLUARD_SECRET_KEY`.
3. Kör **aldrig** `KLUARD_DEV_MODE=true` på ett nätverk du inte kontrollerar: det stänger av inloggningen.
4. Använd starka och separata lösenord, och ta bort API-nycklar och användare du inte längre använder.
5. Ge bara `--docker` till maskiner där åtkomst till Docker-socketen är acceptabel.
6. Uppgradera Kluard minst varje månad och direkt efter en säkerhetsnotis ([UPGRADING.md](../UPGRADING.md)). Uppdatera agenterna också (Hosts-sidan markerar dem som avviker).
7. Skydda backupmappen och serverns disk.
8. Behöver du MFA eller SSO, använd en identitetsmedveten proxy eller Enterprise-utgåvan.

## 11. Kända begränsningar

- Ingen inbyggd MFA i community-utgåvan.
- Räknarna för inloggningsbegränsning ligger i minnet och nollställs vid omstart. Bakom en proxy som inte för vidare klientadressen delar alla klienter proxyns adress för begränsningen av API-nyckelinloggning.
- Webbgränssnittets innehållspolicy tillåter inbäddade skript.
- Data i vila krypteras inte av Kluard.
- En komprometterad Kluard-server kan dela ut en skadlig agentuppdatering.
- Ingen oberoende granskning har gjorts.
