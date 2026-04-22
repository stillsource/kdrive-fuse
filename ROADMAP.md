# Roadmap

## ✅ Done

### Rename / Move
`NodeRenamer` sur `DirNode`. Endpoints `POST /files/{id}/rename` et `POST /files/{id}/move/{destID}`. Cross-dir rename = move puis rename. Invalide cache des deux parents.

### Streaming downloads via disk cache
`api.DownloadStream(ctx, fileID, off, length)` avec header `Range`. `readHandle` ouvre le fichier depuis le disk cache (download full au 1er accès, sert depuis disque ensuite). Perf : 887ms → 1ms sur PDF 641 KB.

### Écriture fichiers existants
`FileNode.Open(O_WRONLY|O_RDWR)` retourne un `writeHandle` seedé avec le contenu distant (sauf O_TRUNC). Upload via mode edit (`file_id` query param au lieu de `file_name`/`directory_id`). `NodeSetattrer` no-op pour accepter truncate-on-open (sinon ENOTSUP).

### Pagination Readdir
`api/list.go` loop sur `page` jusqu'à ce qu'une page retourne < 500 items. Plus de silent-fail.

### Cache disque LRU
`~/.cache/kdrive-fuse/{fileID}_{last_modified_at}`. Invalidation implicite par clé mtime (nouveau mtime = nouveau fichier). LRU par atime (ModTime). Budget via `KDRIVE_DISK_CACHE_MAX_GB` (default 2 GB). Évince à l'écriture si dépassement.

---

## Critiques restantes

### Upload chunked (TUS-like session)
Fichiers > 100 MB : kDrive supporte session `POST /upload/session/start` → `POST /upload/session/{token}/chunk` × N → `POST /upload/session/{token}/finish`. Actuellement tout en single-shot → risque timeout / bufferisation RAM.
Détail : `desktop-kDrive/src/libsyncengine/jobs/network/kDrive_API/upload/upload_session/`.

---

## UX

### `kdshare <path>` CLI
Générer lien public via `POST /files/{fileID}/shares`. Endpoint déjà utilisé dans `portfolio-astro`. Sous-commande `kdrive-fuse share <path>` ou nouveau binaire.

### `.trash/` virtual dir
Corbeille kDrive sous `~/kDrive-vfs/.trash/`. Endpoint `GET /files/trash`. `rm .trash/x` = purge définitif, `mv .trash/x .` = restore.

### Conflict surfacing
Upload avec `conflict=error` actuellement → échec si duplicate. Alternative : `conflict=version` (nouvelle version) ou `conflict=rename` (suffixe "(1)"). Choix selon contexte.

---

## Hygiene

### Setattr utimens → last_modified_at
Actuellement Setattr accepte mais ne persiste rien côté kDrive. Mapper `in.Atime/Mtime` vers un update API distant pour que `touch` marche vraiment.

### xattrs kDrive metadata
`NodeGetxattrer` / `NodeListxattrer` :
- `user.kdrive.id` → ID numérique
- `user.kdrive.mime_type`
- `user.kdrive.created_by`
- `user.kdrive.share_url` (lazy)

### Flag `--readonly`
Env `KDRIVE_READONLY=1` → Create/Mkdir/Unlink/Rmdir/Rename retournent EROFS.

### Structured logs JSON
`slog.NewJSONHandler` au lieu de Text → grepable avec jq.

### Retry idempotency pour ops non-idempotentes
Upload/Create désactivent retry auto (body reader consommé). OK actuel. Vérifier que Rename/Move sont idempotents (déjà renamed → 2ème appel OK ou 404).

---

## Bonus

### Recherche full-text
`GET /files/search?q=...` exposé via `~/kDrive-vfs/.search/{query}/`.

### Multi-drive
Monter plusieurs drives sous `/mnt/kdrive/{drive_id}/`. `KDriveFS` par drive.

### Prometheus metrics
Side-car HTTP `/metrics`. Counters : `kdrive_api_requests_total{op,status}`, `kdrive_bytes_uploaded`, `kdrive_cache_hit_ratio`.
