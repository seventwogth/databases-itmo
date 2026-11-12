-- join_lobby: проверка already_in_session (P0107) — пользователь не может войти в лобби,
-- если уже состоит в другой незавершённой сессии (как в create_lobby).
-- Источник — docs/sql-functions.md, раздел «2. Лобби».

CREATE OR REPLACE FUNCTION join_lobby(p_session_id integer, p_user_id integer) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_status    session_status;
    v_player_id integer;
BEGIN
    SELECT status INTO v_status FROM game_sessions WHERE session_id = p_session_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status <> 'LOBBY_OPEN' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    IF (SELECT count(1) FROM players WHERE session_id = p_session_id) >= 4 THEN
        RAISE EXCEPTION 'lobby_full' USING ERRCODE = 'P0103';
    END IF;
    IF EXISTS (SELECT 1 FROM players WHERE session_id = p_session_id AND user_id = p_user_id) THEN
        RAISE EXCEPTION 'already_joined' USING ERRCODE = 'P0104';
    END IF;
    IF EXISTS (SELECT 1 FROM players p JOIN game_sessions s USING (session_id)
               WHERE p.user_id = p_user_id AND s.status <> 'FINISHED') THEN
        RAISE EXCEPTION 'already_in_session' USING ERRCODE = 'P0107';
    END IF;
    INSERT INTO players (user_id, session_id, turn_at, total_points)
    SELECT p_user_id, p_session_id, coalesce(max(turn_at), 0) + 1, 0
    FROM players WHERE session_id = p_session_id
    RETURNING player_id INTO v_player_id;
    RETURN v_player_id;
END;
$$;
