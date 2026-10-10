# qdrantcli

Kleines CLI, mit dem deine Harness (Claude Code, Codex, …) die Qdrant-Instanz der Firma abfragt – **mit deinem eigenen Token**. Was du sehen darfst, bestimmt allein dein Token (JWT oder API-Key). Eine einzelne Binärdatei ohne Abhängigkeiten, für Windows, macOS und Linux.

## Voraussetzungen

| Was | Wofür |
| --- | --- |
| URL der Qdrant-Instanz der Firma | Verbindung (oft nur im Büro oder per VPN erreichbar) |
| Persönlicher Qdrant-Token (API-Key oder JWT) | Rechte kommen aus diesem Token |
| für `search` mit Text: Embedding-Endpunkt (OpenAI-kompatibel) oder Qdrant-Server-Inferenz | dasselbe Modell, mit dem die Collections gefüllt wurden |

Betriebssysteme: Windows 10/11, macOS 12+, Linux (amd64/arm64). Keine weiteren Laufzeitabhängigkeiten (ein einzelnes Go-Programm).

## Installation

1. Passendes Archiv aus dem Release herunterladen:
   - Windows: `qdrantcli_0.1.0_windows_amd64.zip` (oder `_arm64`)
   - macOS: `qdrantcli_0.1.0_darwin_arm64.tar.gz` (Apple Silicon) bzw. `_darwin_amd64` (Intel)
   - Linux: `qdrantcli_0.1.0_linux_amd64.tar.gz` (oder `_arm64`)
2. Prüfsumme vergleichen: `sha256sum -c SHA256SUMS --ignore-missing`
3. Entpacken und in den `PATH` legen:
   - Linux/macOS: `tar xzf qdrantcli_*.tar.gz && mv qdrantcli ~/.local/bin/` (macOS ggf. `xattr -d com.apple.quarantine ~/.local/bin/qdrantcli`)
   - Windows: `qdrantcli.exe` z. B. nach `%USERPROFILE%\bin` kopieren und den Ordner in den Benutzer-`PATH` aufnehmen.
4. Test: `qdrantcli version`

## Login

```sh
qdrantcli login --url https://qdrant.firma.example
# Token wird ohne Echo abgefragt
```

Die Konfiguration landet in `<UserConfigDir>/qdrantcli/config.json` (Rechte 0600):
Linux `~/.config/qdrantcli/`, macOS `~/Library/Application Support/qdrantcli/`, Windows `%AppData%\qdrantcli\`.

Ohne Datei geht es auch per Umgebung: `QDRANTCLI_URL`, `QDRANTCLI_TOKEN` (überschreiben die Datei).
Optional für die Textsuche: `--embed-url`, `--embed-model`, `--server-model` beim Login speichern.

## Befehle

Jeder Befehl kennt `--help` (mit Beispielen) und `--json` (maschinenlesbar).

| Befehl | Zweck |
|---|---|
| `login` | URL + Token speichern, Verbindung prüfen |
| `whoami` | Verbindung, Token-Typ, sichtbare Collections |
| `collections` | Collections mit Punktanzahl |
| `info <collection>` | Vektor-Config, Payload-Schema, Punktanzahl |
| `scroll <collection>` | Punkte per Payload-Filter |
| `search <collection> "<text>"` | Semantische Suche |
| `version` | Version |

```sh
qdrantcli whoami
qdrantcli collections --json
qdrantcli info dokumente
qdrantcli scroll dokumente --filter abteilung=hr --filter jahr=2026 --limit 5 --fields titel,quelle
qdrantcli scroll dokumente --offset 42          # nächste Seite
```

Filter `key=value` sind UND-verknüpft (Qdrant `must`/`match`); `true`/`false` und Ganzzahlen werden typisiert.

### Suche

Für eine Textsuche braucht es einen Vektor. Drei Wege:

```sh
# a) OpenAI-kompatibler Embedding-Endpunkt (Key in QDRANTCLI_EMBED_KEY)
export QDRANTCLI_EMBED_KEY=...
qdrantcli search dokumente "Reisekosten Richtlinie" --embed-url https://llm.firma.example --embed-model text-embedding-3-small

# b) Inferenz direkt im Qdrant-Server
qdrantcli search dokumente "Reisekosten" --server-model sentence-transformers/all-minilm-l6-v2

# c) roher Vektor als JSON-Array
qdrantcli search dokumente --vector-file vec.json --filter abteilung=hr --limit 5
```

Bei benannten Vektoren: `--using <name>`. Dauerhaft speichern:
`qdrantcli login --url ... --embed-url ... --embed-model ...`

## Sicherheit

- **Dein Token gehört nur dir.** Nie in Brain, Notizen, Chat, Tickets, Prompts oder Git schreiben – auch nicht der Harness „zum Merken“ geben. Die Harness ruft `qdrantcli` auf, das Token liest das CLI selbst aus der Konfig.
- Login am besten interaktiv (Token wird ohne Echo gelesen) statt per `--token` (landet sonst in der Shell-History).
- Konfigdatei hat Rechte 0600. Token kompromittiert? Sofort beim Admin sperren lassen.
- Rechte kommen ausschließlich aus dem Token; `Kein Zugriff mit deinem Token auf <collection>` heißt: dein Token darf das nicht.

## Selbst bauen

Go ≥ 1.22, keine Abhängigkeiten:

```sh
go build -o qdrantcli .
./build.sh 0.1.0      # alle Plattformen nach dist/ inkl. SHA256SUMS
```

## Lizenz

MIT, siehe [LICENSE](LICENSE).
