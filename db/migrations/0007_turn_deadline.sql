ALTER TABLE turns
    ADD COLUMN deadline_at timestamptz NOT NULL DEFAULT (clock_timestamp() + interval '60 seconds');

CREATE FUNCTION turn_remaining_seconds(p_turn_id integer) RETURNS integer
LANGUAGE sql STABLE AS $$
    SELECT greatest(0, ceil(extract(epoch FROM (t.deadline_at - statement_timestamp())))::integer)
    FROM turns t
    WHERE t.turn_id = p_turn_id;
$$;
