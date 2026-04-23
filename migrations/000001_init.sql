
BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  password_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS books (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title text NOT NULL,
  author text,
  source_path text NOT NULL,
  source_checksum text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS books_owner_source_checksum_uniq
  ON books(owner_user_id, source_checksum);

CREATE TABLE IF NOT EXISTS ocr_pages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  book_id uuid NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  page_number integer NOT NULL,
  language text NOT NULL DEFAULT 'ru',
  text text NOT NULL,
  text_checksum text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (book_id, page_number)
);

CREATE TABLE IF NOT EXISTS chunks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  book_id uuid NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  page_start integer NOT NULL,
  page_end integer NOT NULL,
  chunk_index integer NOT NULL,
  text text NOT NULL,
  token_count integer NOT NULL,
  text_checksum text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (book_id, page_start, page_end, chunk_index)
);

CREATE TABLE IF NOT EXISTS chunk_embeddings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  chunk_id uuid NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
  embedding_model text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  embedding vector(1536) NOT NULL,
  UNIQUE (chunk_id, embedding_model)
);

CREATE TABLE IF NOT EXISTS book_summaries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  book_id uuid NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  llm_model text NOT NULL,
  prompt_version text NOT NULL,
  summary_text text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (book_id, prompt_version)
);

CREATE INDEX IF NOT EXISTS ocr_pages_book_page_idx ON ocr_pages (book_id, page_number);
CREATE INDEX IF NOT EXISTS chunks_book_range_idx ON chunks (book_id, page_start, page_end);
CREATE INDEX IF NOT EXISTS chunk_embeddings_model_idx ON chunk_embeddings (embedding_model);

ALTER TABLE chunks
  ADD COLUMN IF NOT EXISTS text_tsv tsvector
  GENERATED ALWAYS AS (to_tsvector('russian', text)) STORED;

CREATE INDEX IF NOT EXISTS chunks_text_tsv_gin_idx ON chunks USING GIN (text_tsv);

CREATE INDEX IF NOT EXISTS chunk_embeddings_embedding_ivfflat_idx
  ON chunk_embeddings
  USING ivfflat (embedding vector_cosine_ops)
  WITH (lists = 100);

COMMIT;

