ALTER TABLE bridge.dispatch_observation
    ADD COLUMN IF NOT EXISTS phase TEXT NOT NULL DEFAULT ''
    CHECK (phase IN ('', 'connect', 'headers', 'content', 'terminal'));

ALTER TABLE bridge.dispatch_observation
    ADD COLUMN IF NOT EXISTS protocol_fact TEXT NOT NULL DEFAULT ''
    CHECK (protocol_fact = '' OR
        (protocol_fact IN ('semantic_failure', 'incomplete') AND outcome = 'unknown' AND phase = 'terminal') OR
        (protocol_fact = 'non_text_terminal' AND outcome IN ('success', 'unknown') AND phase = 'terminal' AND ttft_millis = 0) OR
        (protocol_fact = 'stream_break' AND outcome = 'unknown' AND phase IN ('headers', 'content')) OR
        (protocol_fact = 'local_timeout' AND outcome = 'unknown' AND phase IN ('connect', 'headers', 'content')) OR
        (protocol_fact = 'bridge_deadline' AND outcome = 'client_canceled'));

ALTER TABLE bridge.dispatch_observation
    ADD COLUMN IF NOT EXISTS usage_missing BOOLEAN NOT NULL DEFAULT FALSE
    CHECK (NOT usage_missing OR
        (outcome IN ('success', 'unknown') AND phase = 'terminal' AND protocol_fact IN ('', 'non_text_terminal')));
