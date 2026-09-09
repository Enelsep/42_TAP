# 42_TAP
A shared-world retro text adventure


Pour que le projet fonctionne :

1] installer go 1.27

2] cloner le repo

3] go work init ./core puis go work use ./gui à la racine du repo

4] vérifier que go run ./core/cmd/server renvoie tap server: hello

## Client CLI

Le sujet laisse le choix entre un client qui parle le RFC brut et un client
qui traduit une syntaxe plus naturelle vers le RFC. On a pris **le second
choix** (T5.2) : `core/cmd/cli` traduit `go north` en `MOVE north`, `say hi`
en `CHAT ROOM hi` (et `shout`/`gsay` pour GLOBAL/GROUP), et met en forme les
réponses JSON (LOOK, STATUS, ATTACK, QUESTS...) et les événements avec un peu
de couleur ANSI (`\x1b[...m`, pas de lib externe). Tout ce que la traduction
ne reconnaît pas part tel quel sur le fil, donc la syntaxe RFC complète
fonctionne toujours directement (`MOVE north`, `CHAT GLOBAL hi`, etc. — les
verbes RFC sont insensibles à la casse).

Lancer le client :

```
go run ./core/cmd/cli -addr localhost:4241
```

Le flag `-raw` désactive traduction et mise en forme et retombe sur le
comportement brut de T5.1 (utile pour tester le fil directement, ou pour
comparer avec un client d'un autre groupe).
