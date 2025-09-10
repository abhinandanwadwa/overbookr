ALTER TABLE seat_holds
ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now();

UPDATE seat_holds SET updated_at = created_at WHERE updated_at IS NULL;
