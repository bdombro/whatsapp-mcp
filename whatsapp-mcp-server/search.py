"""
Hybrid search combining FTS5 BM25 keyword search with vector similarity.

Architecture:
  - BM25 keyword search via SQLite FTS5 (index maintained by the Go CLI)
  - Semantic vector search via fastembed (ONNX-based, local, no API keys)
  - Reciprocal Rank Fusion (RRF) to merge both ranked lists
  - Embeddings cached in a separate search.db file
"""

import sqlite3
import struct
import threading
import os.path
from typing import List, Tuple, Optional, Dict

import numpy as np

MESSAGES_DB_PATH = os.path.join(os.path.expanduser('~'), '.local', 'share', 'whatsapp-cli', 'messages.db')
SEARCH_DB_PATH = os.path.join(os.path.expanduser('~'), '.local', 'share', 'whatsapp-cli', 'search.db')

EMBEDDING_DIM = 384
EMBEDDING_MODEL = "BAAI/bge-small-en-v1.5"
EMBED_BATCH_SIZE = 256

_model = None
_model_lock = threading.Lock()


def _get_model():
    global _model
    if _model is None:
        with _model_lock:
            if _model is None:
                from fastembed import TextEmbedding
                _model = TextEmbedding(EMBEDDING_MODEL)
    return _model


def _pack_embedding(vec: np.ndarray) -> bytes:
    return vec.astype(np.float32).tobytes()


def _unpack_embedding(blob: bytes) -> np.ndarray:
    return np.frombuffer(blob, dtype=np.float32)


def _init_search_db():
    conn = sqlite3.connect(SEARCH_DB_PATH)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA busy_timeout=5000")
    conn.execute("""
        CREATE TABLE IF NOT EXISTS embeddings (
            message_id TEXT NOT NULL,
            chat_jid TEXT NOT NULL,
            embedding BLOB NOT NULL,
            PRIMARY KEY (message_id, chat_jid)
        )
    """)
    conn.commit()
    return conn


def _ensure_embeddings():
    """Compute and cache embeddings for any messages not yet embedded."""
    msg_conn = sqlite3.connect(f"file:{MESSAGES_DB_PATH}?mode=ro", uri=True)
    search_conn = _init_search_db()

    try:
        # Find messages that need embedding
        existing = set()
        for row in search_conn.execute("SELECT message_id, chat_jid FROM embeddings"):
            existing.add((row[0], row[1]))

        rows = msg_conn.execute(
            "SELECT id, chat_jid, content FROM messages WHERE content IS NOT NULL AND content != ''"
        ).fetchall()

        to_embed = [(r[0], r[1], r[2]) for r in rows if (r[0], r[1]) not in existing]
        if not to_embed:
            return

        model = _get_model()

        for i in range(0, len(to_embed), EMBED_BATCH_SIZE):
            batch = to_embed[i:i + EMBED_BATCH_SIZE]
            texts = [row[2] for row in batch]
            embeddings = list(model.embed(texts))

            for (msg_id, chat_jid, _), emb in zip(batch, embeddings):
                blob = _pack_embedding(np.array(emb))
                search_conn.execute(
                    "INSERT OR REPLACE INTO embeddings (message_id, chat_jid, embedding) VALUES (?, ?, ?)",
                    (msg_id, chat_jid, blob)
                )
            search_conn.commit()
    finally:
        msg_conn.close()
        search_conn.close()


def _load_embeddings() -> Tuple[List[Tuple[str, str]], np.ndarray]:
    """Load all cached embeddings into memory. Returns (keys, matrix)."""
    search_conn = _init_search_db()
    try:
        rows = search_conn.execute("SELECT message_id, chat_jid, embedding FROM embeddings").fetchall()
        if not rows:
            return [], np.empty((0, EMBEDDING_DIM), dtype=np.float32)
        keys = [(r[0], r[1]) for r in rows]
        matrix = np.stack([_unpack_embedding(r[2]) for r in rows])
        return keys, matrix
    finally:
        search_conn.close()


def bm25_search(
    query: str,
    limit: int = 50,
    chat_jid: Optional[str] = None,
    after: Optional[str] = None,
    before: Optional[str] = None,
) -> List[Tuple[str, str, float]]:
    """
    BM25 keyword search via FTS5.
    Returns list of (message_id, chat_jid, bm25_score) sorted by relevance.
    """
    conn = sqlite3.connect(f"file:{MESSAGES_DB_PATH}?mode=ro", uri=True)
    try:
        # FTS5 MATCH syntax: terms are implicitly ANDed.
        # Escape double quotes in query to prevent injection.
        safe_query = query.replace('"', '""')
        # Use column filter to search only content
        fts_query = f'"{safe_query}"'

        sql_parts = ["""
            SELECT m.id, m.chat_jid, bm25(messages_fts) as score
            FROM messages_fts
            JOIN messages m ON messages_fts.rowid = m.rowid
            WHERE messages_fts MATCH ?
        """]
        params: list = [fts_query]

        if chat_jid:
            sql_parts.append("AND m.chat_jid = ?")
            params.append(chat_jid)
        if after:
            sql_parts.append("AND m.timestamp > ?")
            params.append(after)
        if before:
            sql_parts.append("AND m.timestamp < ?")
            params.append(before)

        sql_parts.append("ORDER BY bm25(messages_fts)")
        sql_parts.append("LIMIT ?")
        params.append(limit)

        rows = conn.execute(" ".join(sql_parts), params).fetchall()
        return [(r[0], r[1], r[2]) for r in rows]
    except sqlite3.OperationalError:
        # FTS5 table might not exist yet (old binary without FTS5 support)
        return []
    finally:
        conn.close()


def vector_search(
    query: str,
    limit: int = 50,
    chat_jid: Optional[str] = None,
) -> List[Tuple[str, str, float]]:
    """
    Semantic vector search via cosine similarity.
    Returns list of (message_id, chat_jid, similarity_score) sorted by relevance.
    """
    _ensure_embeddings()

    model = _get_model()
    query_emb = np.array(list(model.query_embed(query))[0], dtype=np.float32)

    keys, matrix = _load_embeddings()
    if len(keys) == 0:
        return []

    # Cosine similarity: dot product of normalized vectors
    norms = np.linalg.norm(matrix, axis=1, keepdims=True)
    norms = np.where(norms == 0, 1, norms)
    normalized = matrix / norms

    query_norm = np.linalg.norm(query_emb)
    if query_norm > 0:
        query_emb = query_emb / query_norm

    similarities = normalized @ query_emb

    # Apply chat_jid filter if specified
    if chat_jid:
        mask = np.array([k[1] == chat_jid for k in keys], dtype=bool)
        similarities = np.where(mask, similarities, -1.0)

    top_indices = np.argsort(similarities)[::-1][:limit]
    results = []
    for idx in top_indices:
        score = float(similarities[idx])
        if score <= 0:
            break
        results.append((keys[idx][0], keys[idx][1], score))

    return results


def hybrid_search(
    query: str,
    limit: int = 20,
    chat_jid: Optional[str] = None,
    after: Optional[str] = None,
    before: Optional[str] = None,
    k: int = 60,
) -> List[Tuple[str, str, float]]:
    """
    Hybrid search using Reciprocal Rank Fusion (RRF) to combine BM25 and
    vector search results.

    Args:
        query: Search query string
        limit: Max results to return
        chat_jid: Optional filter to a specific chat
        after: Optional ISO-8601 lower bound on timestamp
        before: Optional ISO-8601 upper bound on timestamp
        k: RRF constant (default 60)

    Returns:
        List of (message_id, chat_jid, rrf_score) sorted by relevance.
    """
    # Fetch more candidates than needed so RRF has good coverage
    candidate_limit = limit * 5

    bm25_results = bm25_search(query, limit=candidate_limit, chat_jid=chat_jid, after=after, before=before)
    vec_results = vector_search(query, limit=candidate_limit, chat_jid=chat_jid)

    # RRF scoring: score(d) = sum(1 / (k + rank_i))
    scores: Dict[Tuple[str, str], float] = {}

    for rank, (msg_id, cjid, _) in enumerate(bm25_results):
        key = (msg_id, cjid)
        scores[key] = scores.get(key, 0.0) + 1.0 / (k + rank + 1)

    for rank, (msg_id, cjid, _) in enumerate(vec_results):
        key = (msg_id, cjid)
        scores[key] = scores.get(key, 0.0) + 1.0 / (k + rank + 1)

    ranked = sorted(scores.items(), key=lambda x: x[1], reverse=True)[:limit]
    return [(key[0], key[1], score) for key, score in ranked]


if __name__ == "__main__":
    import sys

    msg_conn = sqlite3.connect(f"file:{MESSAGES_DB_PATH}?mode=ro", uri=True)
    total = msg_conn.execute(
        "SELECT COUNT(*) FROM messages WHERE content IS NOT NULL AND content != ''"
    ).fetchone()[0]
    msg_conn.close()

    search_conn = _init_search_db()
    existing = search_conn.execute("SELECT COUNT(*) FROM embeddings").fetchone()[0]
    search_conn.close()

    remaining = total - existing
    if remaining <= 0:
        print(f"Search index up to date ({existing} messages indexed).")
        sys.exit(0)

    print(f"Computing embeddings for {remaining} messages ({existing} already indexed)...")
    _ensure_embeddings()

    search_conn = _init_search_db()
    final_count = search_conn.execute("SELECT COUNT(*) FROM embeddings").fetchone()[0]
    search_conn.close()
    print(f"Search index complete: {final_count} messages indexed.")
