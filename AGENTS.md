Grundregel für Code-Aenderungen an der Chain

Jede Aenderung am Code der Chain (insbesondere im Bereich `jsonrpc/`, `core/`, `contracts/`, `state/`, `types/`) **muss** vor Uebergabe, Review oder Commit folgenden Mindestanforderungen genuegen:
Der gesamte Codebaum laesst sich erfolgreich bauen:

```bash
go build ./...
