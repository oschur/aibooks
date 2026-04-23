BEGIN;

DROP INDEX IF EXISTS chunk_embeddings_embedding_ivfflat_idx;
DROP INDEX IF EXISTS chunks_text_tsv_gin_idx;
DROP INDEX IF EXISTS chunk_embeddings_model_idx;
DROP INDEX IF EXISTS chunks_book_range_idx;
DROP INDEX IF EXISTS ocr_pages_book_page_idx;
DROP INDEX IF EXISTS books_owner_source_checksum_uniq;

DROP TABLE IF EXISTS book_summaries;
DROP TABLE IF EXISTS chunk_embeddings;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS ocr_pages;
DROP TABLE IF EXISTS books;
DROP TABLE IF EXISTS users;

COMMIT;
