-- +goose Up
-- Без этих индексов анти-джойны фильтра unused и массового удаления идут
-- полным сканом students и tts_jobs на каждый запрос.
CREATE INDEX students_avatar_media_id_idx
    ON students (avatar_media_id) WHERE avatar_media_id IS NOT NULL;

CREATE INDEX tts_jobs_media_id_idx
    ON tts_jobs (media_id) WHERE media_id IS NOT NULL;

-- +goose Down
DROP INDEX tts_jobs_media_id_idx;
DROP INDEX students_avatar_media_id_idx;
