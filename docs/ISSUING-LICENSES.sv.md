# Rutin: utfärda licenser (Enterprise, Operator och prov)

Gäller `cmd/kluard-license`. Allt sker på **er egen dator**, inte på en server som kunder når.

## Viktigt först

- **Operator ingår inte i Enterprise.** Det är två separata utgåvor i licensfilen: `enterprise` och `operator`. Kunden får bara det som står i filen. En hostare som vill ha både tenant-konsolen och fakturering/SSO behöver licens med **båda**: `--editions enterprise,operator`.
- Enterprise-funktioner: fakturering, OIDC, grupper, ärendeintegrationer. Operator-funktioner: Tenants-konsolen, gränser per tenant, avstängning, support-åtkomst, export/borttagning.
- Allt verifieras offline mot er publika nyckel (`internal/license/keys/kluard.pub`, inbyggd i bygget). Den privata nyckeln (`*.key`) får aldrig ligga på en server, i repot eller i CI.
- Gränserna (`--hosts`, `--tenants`) är **mjuka**: Kluard varnar vid 90 procent och när de passeras, men nekar aldrig data.

## Installations-id (betalande kunder)

En betald licens **binds till kundens installations-id**. Kunden ser det i klartext i Kluard under **Settings, Licence** ("Installation id", med kopieringsknapp) och finns redan från första start, även utan licens. Be kunden skicka det (format `KLU-7K2M-9QXA-4HDP-WE3T`) innan du utfärdar. En bunden licens fungerar bara där; på en annan installation nekas den med ett meddelande som nämner båda id:n. Provlicenser binds inte. Alla exempel nedan med `issue` kräver `--installation KLU-...` (eller `--unbound` för ett undantag som fungerar överallt).

## 0. Engångsförberedelse (görs bara första gången)

```bash
cd ~/kluard
go run ./cmd/kluard-license keygen --id kluard --dir keys
cp keys/kluard.pub internal/license/keys/kluard.pub
git add internal/license/keys/kluard.pub && git commit -s -m "Add the licence verification key"
```

Spara `keys/kluard.key` offline (krypterat USB-minne) plus en andra krypterad kopia på annan plats. Har ni redan nyckeln, hoppa över detta steg. Nyckelns namn (`--id`) måste vara `kluard`, annars litar bygget inte på licenserna.

## 1. Provlicens (30 dagar, alla funktioner)

```bash
cd ~/kluard
go run ./cmd/kluard-license trial --key keys/kluard.key --key-id kluard \
  --customer "KUNDENS BOLAG AB" --ledger issued.jsonl --out kundens-bolag-trial.license
```

- Ger **enterprise + operator** i 30 dagar, utan frist efter slutdatum (ingen "grace"). Påminnelse en vecka före slut.
- Ändra längd med `--days 14`, begränsa med `--editions enterprise`, sätt mjuka gränser med `--hosts 20 --tenants 5`.
- Efter provet är installationen Community igen, med all data kvar.

## 2. Enterprise-licens (betald)

```bash
go run ./cmd/kluard-license issue --key keys/kluard.key --key-id kluard \
  --customer "KUNDENS BOLAG AB" --editions enterprise --days 365 \
  --installation KLU-XXXX-XXXX-XXXX-XXXX \
  --hosts 50 --ledger issued.jsonl --out kundens-bolag-enterprise.license
```

## 3. Operator-licens (för hostare)

Operator ensam, om kunden redan har Enterprise via en annan licens, eller (vanligast) båda i en fil:

```bash
# Båda utgåvorna i samma fil (rekommenderas för hostare)
go run ./cmd/kluard-license issue --key keys/kluard.key --key-id kluard \
  --customer "HOSTARE AB" --editions enterprise,operator --days 365 \
  --installation KLU-XXXX-XXXX-XXXX-XXXX \
  --hosts 200 --tenants 50 --ledger issued.jsonl --out hostare-ab.license

# Bara Operator
go run ./cmd/kluard-license issue --key keys/kluard.key --key-id kluard \
  --customer "HOSTARE AB" --editions operator --days 365 \
  --installation KLU-XXXX-XXXX-XXXX-XXXX \
  --tenants 50 --ledger issued.jsonl --out hostare-ab-operator.license
```

`--tenants` är taket för antal tenants (mjukt). `--hosts` är taket för antal maskiner.

## 4. Slutdatum och förnyelse

- `--days 365` räknar från idag. `--expires 2027-12-31` slutar vid dagens slut (UTC).
- Efter slutdatum fungerar Enterprise/Operator i **30 dagar till** med varning, sedan stängs de av. Community och all data påverkas aldrig.
- **Förnyelse = ny licens.** Datera den så den börjar när den gamla slutar: `--issued 2027-12-31 --days 365`. Kunden installerar den över den gamla.

## 5. Kontrollera filen innan du skickar den

```bash
go run ./cmd/kluard-license inspect kundens-bolag-enterprise.license --pub keys/kluard.pub
```

Kontrollera kund, utgåvor (`enterprise`, `operator`), slutdatum och gränser.

## 6. Skicka till kunden

Skicka `.license`-filen som den är (den är inte hemlig men gjord för just dem). Be kunden aktivera den på ett av två sätt:

- I Kluard: **Settings**, klistra in hela filens innehåll (med klammerparenteser) och spara. Funktionerna slås på inom en minut, utan omstart.
- Som fil: sätt `KLUARD_LICENSE_FILE=/sökväg/till/filen.license`. Filen läses vid start och varje minut, så en förnyelse kan läggas dit utan omstart.

## 7. Bokföring

`--ledger issued.jsonl` lägger en rad per licens (vem, vad, till när). Filen är er egen logg: säkerhetskopiera den men lägg den **inte** i det publika repot (den ligger redan i `.gitignore`).

## Felsökning

| Meddelande | Orsak och åtgärd |
|---|---|
| "the signature does not match" | Bygget på kundens server har fel prefix eller fel nyckel. Använd samma `internal/license/keys/kluard.pub` som den privata nyckeln hör till, och bygg om. |
| "this build of Kluard trusts no licence keys" | Den publika nyckeln saknas i bygget. Lägg `kluard.pub` i `internal/license/keys/` och bygg om. |
| "signed with the key … which this build does not trust" | `--key-id` i licensen matchar inte någon inbyggd nyckel. Utfärda med `--key-id kluard`. |
| Tenants-sidan saknas trots Enterprise | Licensen saknar `operator`. Utfärda ny med `--editions enterprise,operator`. |
| Fakturering saknas trots Operator | Licensen saknar `enterprise`. Samma sak. |
| "this licence was issued for the installation X, but this installation is Y" | Licensen är bunden till ett annat installations-id. Be om id:t under Settings, Licence och utfärda en ny med rätt `--installation`. |
| "the licence in force is read from the file …" | `KLUARD_LICENSE_FILE` är satt: byt den filen i stället för att klistra in i Settings. |
