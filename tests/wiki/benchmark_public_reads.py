# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Compare Wiki readers against a disposable MySQL server on WIKI_TEST_PORT."""

import asyncio
import json
from contextlib import asynccontextmanager
from math import ceil
from statistics import median
from time import perf_counter

from core.wiki.reader import PublicReader
from tests.wiki import test_mysql


async def run():
    async with asynccontextmanager(test_mysql.store.__wrapped__)() as store:
        await store.query("CREATE TABLE digits (n INT PRIMARY KEY)")
        await store.query("INSERT INTO digits VALUES (0),(1),(2),(3),(4),(5),(6),(7),(8),(9)")
        for channel_id in range(12001, 12009):
            ch, _, _, revision, publication = await test_mysql.published(
                store, channel_id=channel_id
            )
            # Deterministic IDs with indexed references and one authorized source per revision.
            sequence = (
                "SELECT a.n+10*b.n+100*d.n n FROM digits a CROSS JOIN digits b CROSS JOIN digits d"
            )
            prefix = f"{channel_id:08x}"
            page_id = f"CONCAT('{prefix}', LPAD(HEX(seq.n),24,'0'))"
            rev_id = f"CONCAT('{prefix}', LPAD(HEX(seq.n+1000),24,'0'))"
            await store.query(
                f"""INSERT INTO wiki_pages (id,channel_id,entity_key,title,slug,kind,topic,updated_at)
                SELECT {page_id}, %s, SHA2({page_id},256), 'Sakura test', CONCAT('bench-',seq.n), 'Topic',
                'Guide', UTC_TIMESTAMP(6) FROM ({sequence}) seq""",
                (ch["id"],),
            )
            await store.query(
                f"""INSERT INTO wiki_page_revisions
                (id,channel_id,page_id,document,content_hash,search_text,approved,created_at)
                SELECT {rev_id}, %s, {page_id},
                JSON_SET(r.document,'$.claims[0].text',REPEAT('Sakura 中文资料。',400)),
                SHA2({rev_id},256),'Sakura 中文资料。',TRUE,
                UTC_TIMESTAMP(6)-INTERVAL seq.n SECOND
                FROM ({sequence}) seq CROSS JOIN wiki_page_revisions r WHERE r.id=%s""",
                (ch["id"], revision),
            )
            await store.query(
                f"""INSERT INTO wiki_publication_pages (publication_id,page_id,revision_id)
                SELECT %s,{page_id},{rev_id} FROM ({sequence}) seq""",
                (publication,),
            )
            await store.query(
                f"""INSERT INTO wiki_evidence
                (revision_id,source_id,source_hash,chunk_id,start_offset,end_offset)
                SELECT {rev_id},ev.source_id,ev.source_hash,ev.chunk_id,ev.start_offset,ev.end_offset
                FROM ({sequence}) seq CROSS JOIN wiki_evidence ev WHERE ev.revision_id=%s""",
                (revision,),
            )
        await store.query("DROP TABLE digits")
        reader = PublicReader(store)
        query = store.query
        captured = []

        async def timed(sql, args=(), **kwargs):
            start = perf_counter()
            result = await query(sql, args, **kwargs)
            if kwargs.get("fetch") and "wiki_publication_pages" in sql:
                captured.append((sql, args, (perf_counter() - start) * 1000))
            return result

        store.query = timed
        for name, fn in [
            ("legacy", lambda: reader.search(page_size=20)),
            ("latest", lambda: reader.feed()),
            ("recommended", lambda: reader.feed(mode="recommended")),
            ("legacy-search", lambda: reader.search(q="中文", page_size=20)),
            ("cursor-search", lambda: reader.feed(mode="search", q="中文")),
        ]:
            times, sql_times, sizes = [], [], []
            for _ in range(5):
                captured.clear()
                start = perf_counter()
                result = await fn()
                times.append((perf_counter() - start) * 1000)
                sql_times.append(sum(item[2] for item in captured))
                sizes.append(len(json.dumps(result, default=str, ensure_ascii=False).encode()))
            print(
                name,
                "reader_ms",
                round(median(times), 2),
                "reader_p95",
                round(sorted(times)[ceil(len(times) * 0.95) - 1], 2),
                "sql_ms",
                round(median(sql_times), 2),
                "bytes",
                round(median(sizes)),
                "queries",
                len(captured),
                flush=True,
            )
            for sql, args, _ in list(captured):
                plan = await query("EXPLAIN ANALYZE " + sql, args, fetch=True)
                print(next(iter(plan[0].values())), flush=True)


if __name__ == "__main__":
    if not test_mysql.TEST_PORT:
        raise SystemExit("Set WIKI_TEST_PORT to a disposable MySQL server")
    asyncio.run(run())
