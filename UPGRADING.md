# Upgrading Kluard

How to install the latest version, upgrade a running server, and update the agents. You only need **read** access to the repository: nobody has to push anything to upgrade.

- Your settings are kept: `deploy/.env` (address, secrets, bind address) is never replaced, and all data lives in Docker volumes that an upgrade does not touch.
- Several Kluard servers with different addresses are upgraded the same way, each from its own folder.
- See [CHANGELOG.md](CHANGELOG.md) for what changed.

## From Lumen to Kluard

The product was renamed from Lumen to Kluard. A server that runs Lumen keeps its data and settings; only the names of the program, the containers and the repository change.

On each server (Rocky, Ubuntu, a pilot server), in its folder:

```bash
cd /opt/lumen                       # the folder can keep its name
sudo cp deploy/.env ~/kluard-env-backup && sudo chmod 600 ~/kluard-env-backup
docker compose -f deploy/docker-compose.yml down --remove-orphans     # stops Lumen (about a minute); no data is deleted
git remote set-url origin https://github.com/getkluard/kluard.git       # a server with a read key: git@github.com:getkluard/kluard.git
git pull --ff-only
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml ps
```

- `deploy/.env` can stay as it is: the old `LUMEN_*` names are still read. If you want the new names: `sudo sed -i 's/^LUMEN_/KLUARD_/' deploy/.env` (the keys and passwords stay the same; the old `lmn_` API keys keep working).
- `down` stops and removes the containers only. The volumes with your data are not touched (there is no `-v`). It is done before the update because the service is renamed: the old container would otherwise keep the port.
- Everyone is logged out once (the session cookie has a new name).
- The old address `github.com/danielingemar/lumen` and `inmagma/lumen` redirect to the new repository as long as nobody creates a new repository with that name.

**Agents** need nothing at once: an agent from before the rename keeps reporting, and its one-click update works (the server still offers the old file names). To move a machine to the new names (`kluard-agent`, `/etc/kluard-agent`), run the install command from **Add a machine** again, or with `--update`: the old agent is replaced and its settings and key are kept. Docker agents: start them again with the new command from **Add a machine**; the old `LUMEN_AGENT_*` variables also still work.

What deliberately keeps its old name: the ClickHouse database and Elasticsearch index prefix (`lumen`), the Docker volumes `lumendata` and `lumenbackup`, and the internal format names of licence files and of the encryption of stored secrets. They are not visible to users, and renaming them would hide or break what is already stored.

## 1. Before you start (2 minutes)

On the server, in the Kluard folder:

```bash
sudo grep -E '^KLUARD_(PUBLIC_URL|BIND)=' deploy/.env      # note the output; you compare it afterwards
sudo cp deploy/.env ~/kluard-env-backup && sudo chmod 600 ~/kluard-env-backup
```

`deploy/.env` holds `KLUARD_SECRET_KEY`, which decrypts the stored Nextcloud tokens. Keep a copy somewhere safe, away from the server.

For a big upgrade also copy the daily backups off the machine:

```bash
docker compose -f deploy/docker-compose.yml cp kluard:/backup ./kluard-backup
```

## 2. Get the new code

Pick the way that matches your access.

### A. The folder is a git clone

```bash
cd ~/kluard
git status --short          # should print nothing
git pull
```

If `git pull` complains about local changes and you have none you want to keep:

```bash
git fetch origin && git reset --hard origin/main
```

This only resets files git already tracks. `deploy/.env` is ignored by git and is not touched.

### B. You can only read the repository (no push rights)

That is enough. For a public repository nothing is needed:

```bash
git clone https://github.com/getkluard/kluard.git
```

For a private repository, create your **own** GitHub token (classic, scope `repo`; your account has read access, so the token cannot write) and use it as the password when git asks. Never use someone else's token.

For a server, a **deploy key** is cleaner: a key that can only read this repository. The repository owner adds the public key under *Settings → Deploy keys* and leaves *Allow write access* unchecked:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/kluard_deploy -N ""
cat ~/.ssh/kluard_deploy.pub                            # give this to the repository owner
GIT_SSH_COMMAND='ssh -i ~/.ssh/kluard_deploy -o IdentitiesOnly=yes' git clone git@github.com:getkluard/kluard.git
cd kluard && git config core.sshCommand 'ssh -i ~/.ssh/kluard_deploy -o IdentitiesOnly=yes'
```

### C. No git on the server: download a ZIP

On GitHub choose **Code → Download ZIP**, copy it to the server, then:

```bash
cd ~
unzip -q kluard-main.zip
cp -a kluard-main/. kluard/          # overwrites the code, leaves deploy/.env alone (it is not in the ZIP)
cd kluard
chmod +x dev.sh install.sh deploy/*.sh scripts/*.sh
```

## 3. Rebuild and restart

```bash
sudo docker compose -f deploy/docker-compose.yml up -d --build
```

The first build takes a few minutes. Your data stays: ClickHouse (telemetry), Elasticsearch (users, dashboards, settings) and the backup volume are untouched. Changed settings such as the retention period are applied to existing tables when the server starts.

This command never edits `deploy/.env`. The installer scripts are also safe to re-run: `./install.sh` and `./dev.sh` keep the address already in `deploy/.env` and only change it if you pass `--public-url` (or `--ip`).

## 4. Check that it worked

```bash
sudo grep -E '^KLUARD_(PUBLIC_URL|BIND)=' deploy/.env    # identical to what you noted in step 1
sudo docker compose -f deploy/docker-compose.yml ps     # kluard, clickhouse, elasticsearch: Up / healthy
curl -s http://127.0.0.1:4318/healthz                   # prints: ok
sudo docker compose -f deploy/docker-compose.yml logs --tail=20 kluard
```

Then open Kluard in the browser. The page is revalidated on every load, so an upgrade shows at once; if it still looks old, press **Ctrl+F5**. Browsers cache the tab icon hard: close and reopen the tab if you still see the old one.

**Which version is this server running?** The bottom of the menu shows the build, for example `build 3fa91c2d`. It identifies the source code, so every server (and every agent) built from the same code shows the same value. To see what a folder *should* show, run this in the Kluard folder; it must match the menu after a rebuild:

```bash
echo "build $(find . -type f \( -name '*.go' -o -name '*.html' -o -name go.mod \) ! -name '*_test.go' | LC_ALL=C sort | xargs cat | sha256sum | cut -c1-8)"
```

- The menu shows an **older** value than the folder: the server was not rebuilt, or the container was not recreated. Run `sudo docker compose -f deploy/docker-compose.yml up -d --build` again.
- The folder itself is old: `git pull` brought nothing (check `git log --oneline | head -3` against the repository on GitHub, and that the change was pushed).
- `build dev`: the server was built outside Docker (a plain `go build`), so agents are not compared.

## 5. Update the agents

Upgrading the server does **not** update the agents on your monitored machines. Agents keep working, but new agent features (for example the IP address on the Hosts page, services and containers, remote configuration) need the new agent.

**With one click.** The **Hosts** page shows each agent's version. An agent that is not the same build as its server is marked **update**. Click it (or **Update N agents** at the top of the list to do all at once) and the agent updates itself:

- **A Linux machine installed with the install command:** the agent runs as an unprivileged user and cannot replace its own file, so it only asks. A small helper owned by root (started by a systemd path unit, `kluard-agent-update`) downloads the new version from your Kluard server, checks it against the server's checksums, replaces the file, restarts the agent, and puts the old version back if the new one does not stay up. What is installed is only what your server offers; the agent decides nothing but "please update". Look at what happened with `journalctl -u kluard-agent-update`.
- **A Docker container:** the agent downloads the new version, checks the checksum and that the file says it is the version asked for, and replaces its own process. The container keeps running. A restart of the container downloads the latest version anyway.
- **Not Windows:** run the install command again there.

Monitoring of a machine pauses for a few seconds while it restarts. If an update has not happened ten minutes after the click, the label says **update failed?**: look at the log named above on that machine. An update that did not take effect (for example because the server's download folder is out of date) is not retried more than once an hour.

**Agents installed before this feature** cannot update themselves yet. For these, click the label to see the command, and do it by hand **once**; after that a click is enough:

```bash
# Linux, installed with the install command: updates the agent, keeps its settings, needs no key
curl -fsSL https://YOUR-KLUARD/install/agent.sh | sudo sh -s -- --update

# Docker container (it downloads the new version when it starts)
docker restart kluard-agent
```

(Running the install command with `--key` again also works on Linux, but it writes the settings from scratch: any log paths and instances given as flags on that machine are replaced by what you give it.)

## Going back

Data is not changed in a way that stops an older version from starting, but features added later stop working in an older version.

```bash
git log --oneline | head              # find the commit you want
git checkout COMMIT                   # for example the one before the upgrade
sudo docker compose -f deploy/docker-compose.yml up -d --build
```

Return to the latest version with `git checkout main && git pull`.

## Troubleshooting

| Problem | What to do |
|---|---|
| `git pull`: *local changes would be overwritten* | See step 2A: reset to `origin/main` if you have nothing to keep, otherwise `git stash` first. |
| `fatal: detected dubious ownership` | `git config --global --add safe.directory "$PWD"` |
| `Permission denied (publickey)` | The remote uses SSH but this machine has no key. Use the HTTPS address, or a deploy key (step 2B). |
| `fatal: not a git repository` | The folder was unpacked from a ZIP. Use step 2C. |
| The build fails or stops | Check free disk space (`df -h`, `docker system df`) and read the last lines of the build output. |
| Scripts: *Permission denied* | `chmod +x dev.sh install.sh deploy/*.sh scripts/*.sh` |
| The site returns 504 behind nginx | See "Install on a server" in the README. nginx must reach port 4318 of the Kluard server. |
| A machine shows a dash instead of an IP address | Its agent is older than the Kluard server: run the install command again (step 5). |
