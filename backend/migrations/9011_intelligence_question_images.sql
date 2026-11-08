-- 图片与题干一同持久化；旧题重新保存后生成图片。
ALTER TABLE intelligence_questions
    ADD COLUMN IF NOT EXISTS prompt_image TEXT NOT NULL DEFAULT '';
