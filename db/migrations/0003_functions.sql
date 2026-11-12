-- Сгенерировано из docs/sql-functions.md — при изменении функций правьте документацию и этот файл синхронно.

-- ===== 1. Пользователи и вход =====

-- register_user
CREATE FUNCTION register_user(p_login varchar, p_password_hash varchar) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_user_id integer;
BEGIN
    IF coalesce(btrim(p_login), '') = '' OR coalesce(p_password_hash, '') = '' THEN
        RAISE EXCEPTION 'invalid_argument' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (SELECT 1 FROM users WHERE login = p_login) THEN
        RAISE EXCEPTION 'login_already_taken' USING ERRCODE = '23505';
    END IF;
    INSERT INTO users (login, password_hash) VALUES (p_login, p_password_hash)
    RETURNING user_id INTO v_user_id;
    RETURN v_user_id;
END;
$$;

-- find_user_by_login
CREATE FUNCTION find_user_by_login(p_login varchar)
RETURNS TABLE(user_id integer, login varchar, password_hash varchar)
LANGUAGE sql STABLE AS $$
    SELECT u.user_id, u.login, u.password_hash FROM users u WHERE u.login = p_login
$$;

-- touch_last_active
CREATE FUNCTION touch_last_active(p_user_id integer) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE users SET last_active = now() WHERE user_id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'user_not_found' USING ERRCODE = 'P0100';
    END IF;
END;
$$;

-- get_profile
CREATE FUNCTION get_profile(p_user_id integer)
RETURNS TABLE(user_id integer, login varchar, last_active timestamp,
              games_played integer, games_won integer, best_total integer)
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users u WHERE u.user_id = p_user_id) THEN
        RAISE EXCEPTION 'user_not_found' USING ERRCODE = 'P0100';
    END IF;
    RETURN QUERY
    SELECT u.user_id, u.login, u.last_active,
           count(s.session_id)::integer,
           (count(1) FILTER (WHERE p.total_points = m.max_points))::integer,
           coalesce(max(p.total_points) FILTER (WHERE s.session_id IS NOT NULL), 0)::integer
    FROM users u
    LEFT JOIN players p ON p.user_id = u.user_id
    LEFT JOIN game_sessions s ON s.session_id = p.session_id AND s.status = 'FINISHED'
    LEFT JOIN LATERAL (SELECT max(p2.total_points) AS max_points
                       FROM players p2 WHERE p2.session_id = s.session_id) m ON TRUE
    WHERE u.user_id = p_user_id
    GROUP BY u.user_id, u.login, u.last_active;
END;
$$;

-- ===== 3. Игровой процесс: ход =====

-- roll_dice_values
CREATE FUNCTION roll_dice_values(p_count integer) RETURNS integer[]
LANGUAGE plpgsql VOLATILE AS $$
BEGIN
    IF p_count IS NULL OR p_count NOT BETWEEN 1 AND 5 THEN
        RAISE EXCEPTION 'invalid_argument' USING ERRCODE = '22023';
    END IF;
    RETURN ARRAY(SELECT floor(random() * 6)::integer + 1 FROM generate_series(1, p_count));
END;
$$;

-- current_player
CREATE FUNCTION current_player(p_session_id integer) RETURNS integer
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_status session_status;
    v_amount integer;
    v_player integer;
BEGIN
    SELECT status, turn_amount INTO v_status, v_amount
    FROM game_sessions WHERE session_id = p_session_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status = 'FINISHED' THEN
        RAISE EXCEPTION 'session_finished' USING ERRCODE = 'P0302';
    ELSIF v_status <> 'IN_PROGRESS' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    SELECT p.player_id INTO v_player
    FROM players p
    CROSS JOIN LATERAL (SELECT count(1) AS done FROM turns t
                        WHERE t.player_id = p.player_id AND t.is_closed) c
    WHERE p.session_id = p_session_id AND c.done < v_amount
    ORDER BY c.done, p.turn_at
    LIMIT 1;
    IF v_player IS NULL THEN
        RAISE EXCEPTION 'session_finished' USING ERRCODE = 'P0302';
    END IF;
    RETURN v_player;
END;
$$;

-- assert_player_turn
CREATE FUNCTION assert_player_turn(p_player_id integer) RETURNS void
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_session integer;
    v_status  session_status;
BEGIN
    SELECT p.session_id, s.status INTO v_session, v_status
    FROM players p JOIN game_sessions s USING (session_id)
    WHERE p.player_id = p_player_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status = 'FINISHED' THEN
        RAISE EXCEPTION 'session_finished' USING ERRCODE = 'P0302';
    ELSIF v_status <> 'IN_PROGRESS' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    IF current_player(v_session) <> p_player_id THEN
        RAISE EXCEPTION 'not_your_turn' USING ERRCODE = 'P0201';
    END IF;
END;
$$;

-- is_turn_closed
CREATE FUNCTION is_turn_closed(p_turn_id integer) RETURNS boolean
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_closed boolean;
BEGIN
    SELECT is_closed INTO v_closed FROM turns WHERE turn_id = p_turn_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    RETURN v_closed;
END;
$$;

-- turn_dice_values
CREATE FUNCTION turn_dice_values(p_turn_id integer) RETURNS integer[]
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_values integer[];
BEGIN
    IF NOT EXISTS (SELECT 1 FROM turns WHERE turn_id = p_turn_id) THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    SELECT array_agg(value ORDER BY die_id) INTO v_values FROM dice WHERE turn_id = p_turn_id;
    IF cardinality(v_values) IS DISTINCT FROM 5 THEN
        RAISE EXCEPTION 'invalid_argument' USING ERRCODE = '22023';
    END IF;
    RETURN v_values;
END;
$$;

-- turn_score
CREATE FUNCTION turn_score(p_turn_id integer) RETURNS integer
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_score integer;
BEGIN
    SELECT CASE WHEN t.combo_id IS NULL THEN 0
                ELSE calc_score(turn_dice_values(t.turn_id), c.name, c.points, t.throw_number)
           END
    INTO v_score
    FROM turns t LEFT JOIN combinations c USING (combo_id)
    WHERE t.turn_id = p_turn_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    RETURN v_score;
END;
$$;

-- begin_turn
CREATE FUNCTION begin_turn(p_player_id integer)
RETURNS TABLE(turn_id integer, turn_number integer, throw_number integer, dice jsonb)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_amount integer;
    v_done   integer;
    v_turn   integer;
BEGIN
    PERFORM assert_player_turn(p_player_id);
    SELECT s.turn_amount INTO v_amount
    FROM players p JOIN game_sessions s USING (session_id)
    WHERE p.player_id = p_player_id;
    IF EXISTS (SELECT 1 FROM turns t WHERE t.player_id = p_player_id AND NOT t.is_closed) THEN
        RAISE EXCEPTION 'turn_already_open' USING ERRCODE = 'P0207';
    END IF;
    SELECT count(1) INTO v_done FROM turns t WHERE t.player_id = p_player_id;
    IF v_done >= v_amount THEN
        RAISE EXCEPTION 'session_finished' USING ERRCODE = 'P0302';
    END IF;
    INSERT INTO turns (player_id, combo_id, turn_number, throw_number)
    VALUES (p_player_id, NULL, v_done + 1, 1)
    RETURNING turns.turn_id INTO v_turn;
    INSERT INTO dice (turn_id, value, is_fixed)
    SELECT v_turn, x, FALSE FROM unnest(roll_dice_values(5)) AS x;
    RETURN QUERY
    SELECT v_turn, v_done + 1, 1,
           (SELECT jsonb_agg(jsonb_build_object('die_id', d.die_id, 'value', d.value,
                                                'is_fixed', d.is_fixed) ORDER BY d.die_id)
            FROM dice d WHERE d.turn_id = v_turn);
END;
$$;

-- set_dice_fixed
CREATE FUNCTION set_dice_fixed(p_turn_id integer, p_fixed_die_ids integer[])
RETURNS TABLE(die_id integer, value integer, is_fixed boolean)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_player integer;
    v_throw  integer;
    v_closed boolean;
BEGIN
    SELECT t.player_id, t.throw_number, t.is_closed INTO v_player, v_throw, v_closed
    FROM turns t WHERE t.turn_id = p_turn_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    PERFORM assert_player_turn(v_player);
    IF v_closed THEN
        RAISE EXCEPTION 'turn_already_closed' USING ERRCODE = 'P0202';
    END IF;
    IF v_throw >= 3 THEN
        RAISE EXCEPTION 'no_throws_left' USING ERRCODE = 'P0203';
    END IF;
    p_fixed_die_ids := coalesce(p_fixed_die_ids, '{}');
    IF cardinality(p_fixed_die_ids) > 5 OR EXISTS (
        SELECT 1 FROM unnest(p_fixed_die_ids) AS f
        WHERE NOT EXISTS (SELECT 1 FROM dice d WHERE d.die_id = f AND d.turn_id = p_turn_id)
    ) THEN
        RAISE EXCEPTION 'invalid_argument' USING ERRCODE = '22023';
    END IF;
    UPDATE dice d SET is_fixed = (d.die_id = ANY (p_fixed_die_ids)) WHERE d.turn_id = p_turn_id;
    RETURN QUERY
    SELECT d.die_id, d.value, d.is_fixed FROM dice d WHERE d.turn_id = p_turn_id ORDER BY d.die_id;
END;
$$;

-- active_turn
CREATE FUNCTION active_turn(p_user_id integer) RETURNS integer
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_player  integer;
    v_session integer;
    v_turn    integer;
BEGIN
    SELECT p.player_id, p.session_id INTO v_player, v_session
    FROM players p JOIN game_sessions s USING (session_id)
    WHERE p.user_id = p_user_id AND s.status = 'IN_PROGRESS'
    LIMIT 1;
    IF NOT FOUND OR current_player(v_session) <> v_player THEN
        RAISE EXCEPTION 'not_your_turn' USING ERRCODE = 'P0201';
    END IF;
    SELECT t.turn_id INTO v_turn FROM turns t WHERE t.player_id = v_player AND NOT t.is_closed;
    IF v_turn IS NULL THEN
        RAISE EXCEPTION 'not_your_turn' USING ERRCODE = 'P0201';
    END IF;
    RETURN v_turn;
END;
$$;

-- reroll_dice
CREATE FUNCTION reroll_dice(p_user_id integer, p_fixed_die_ids integer[])
RETURNS TABLE(turn_id integer, throw_number integer, dice jsonb)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_turn  integer;
    v_throw integer;
BEGIN
    v_turn := active_turn(p_user_id);
    SELECT t.throw_number INTO v_throw FROM turns t WHERE t.turn_id = v_turn;
    IF v_throw >= 3 THEN
        RAISE EXCEPTION 'no_throws_left' USING ERRCODE = 'P0203';
    END IF;
    PERFORM set_dice_fixed(v_turn, p_fixed_die_ids);
    IF NOT EXISTS (SELECT 1 FROM dice d WHERE d.turn_id = v_turn AND NOT d.is_fixed) THEN
        RAISE EXCEPTION 'all_dice_fixed' USING ERRCODE = 'P0206';
    END IF;
    UPDATE dice d SET value = (roll_dice_values(1))[1]
    WHERE d.turn_id = v_turn AND NOT d.is_fixed;
    UPDATE turns t SET throw_number = t.throw_number + 1 WHERE t.turn_id = v_turn;
    RETURN QUERY
    SELECT v_turn, v_throw + 1,
           (SELECT jsonb_agg(jsonb_build_object('die_id', d.die_id, 'value', d.value,
                                                'is_fixed', d.is_fixed) ORDER BY d.die_id)
            FROM dice d WHERE d.turn_id = v_turn);
END;
$$;

-- get_turn_state
CREATE FUNCTION get_turn_state(p_turn_id integer)
RETURNS TABLE(turn_id integer, turn_number integer, throw_number integer,
              dice jsonb, combo_id integer, score integer)
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
BEGIN
    IF NOT EXISTS (SELECT 1 FROM turns t WHERE t.turn_id = p_turn_id) THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    RETURN QUERY
    SELECT t.turn_id, t.turn_number, t.throw_number,
           (SELECT jsonb_agg(jsonb_build_object('die_id', d.die_id, 'value', d.value,
                                                'is_fixed', d.is_fixed) ORDER BY d.die_id)
            FROM dice d WHERE d.turn_id = t.turn_id),
           t.combo_id,
           turn_score(t.turn_id)
    FROM turns t
    WHERE t.turn_id = p_turn_id;
END;
$$;

-- available_combinations
CREATE FUNCTION available_combinations(p_turn_id integer)
RETURNS TABLE(combo_id integer, name varchar, points integer, potential_score integer)
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
DECLARE
    v_dice  integer[];
    v_throw integer;
BEGIN
    IF is_turn_closed(p_turn_id) THEN
        RAISE EXCEPTION 'turn_already_closed' USING ERRCODE = 'P0202';
    END IF;
    v_dice := turn_dice_values(p_turn_id);
    SELECT t.throw_number INTO v_throw FROM turns t WHERE t.turn_id = p_turn_id;
    RETURN QUERY
    SELECT c.combo_id, c.name, c.points, calc_score(v_dice, c.name, c.points, v_throw)
    FROM combinations c
    WHERE combo_matches(v_dice, c.name)
    ORDER BY 4 DESC, c.combo_id;
END;
$$;

-- finish_turn
CREATE FUNCTION finish_turn(p_turn_id integer, p_combo_id integer)
RETURNS TABLE(score integer, total_points integer)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_player  integer;
    v_session integer;
    v_dice    integer[];
    v_throw   integer;
    v_name    varchar;
    v_points  integer;
BEGIN
    SELECT t.player_id, t.throw_number INTO v_player, v_throw FROM turns t WHERE t.turn_id = p_turn_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    PERFORM assert_player_turn(v_player);
    IF is_turn_closed(p_turn_id) THEN
        RAISE EXCEPTION 'turn_already_closed' USING ERRCODE = 'P0202';
    END IF;
    SELECT c.name, c.points INTO v_name, v_points FROM combinations c WHERE c.combo_id = p_combo_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'combo_not_available' USING ERRCODE = 'P0204';
    END IF;
    v_dice := turn_dice_values(p_turn_id);
    IF NOT combo_matches(v_dice, v_name) THEN
        RAISE EXCEPTION 'combo_not_available' USING ERRCODE = 'P0204';
    END IF;
    -- очки начисляет триггер trg_turns_after_combo
    UPDATE turns t SET combo_id = p_combo_id, is_closed = TRUE WHERE t.turn_id = p_turn_id;
    SELECT s.session_id INTO v_session
    FROM players p JOIN game_sessions s USING (session_id)
    WHERE p.player_id = v_player AND s.status = 'IN_PROGRESS';
    IF FOUND THEN
        PERFORM begin_turn(current_player(v_session));
    END IF;
    RETURN QUERY
    SELECT calc_score(v_dice, v_name, v_points, v_throw), p.total_points
    FROM players p WHERE p.player_id = v_player;
END;
$$;

-- skip_turn
CREATE FUNCTION skip_turn(p_turn_id integer)
RETURNS TABLE(score integer, total_points integer)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_player  integer;
    v_session integer;
    v_throw   integer;
BEGIN
    SELECT t.player_id, t.throw_number INTO v_player, v_throw FROM turns t WHERE t.turn_id = p_turn_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'turn_not_found' USING ERRCODE = 'P0200';
    END IF;
    PERFORM assert_player_turn(v_player);
    IF is_turn_closed(p_turn_id) THEN
        RAISE EXCEPTION 'turn_already_closed' USING ERRCODE = 'P0202';
    END IF;
    IF v_throw <> 3 THEN
        RAISE EXCEPTION 'throws_not_exhausted' USING ERRCODE = 'P0205';
    END IF;
    UPDATE turns t SET is_closed = TRUE WHERE t.turn_id = p_turn_id;
    SELECT s.session_id INTO v_session
    FROM players p JOIN game_sessions s USING (session_id)
    WHERE p.player_id = v_player AND s.status = 'IN_PROGRESS';
    IF FOUND THEN
        PERFORM begin_turn(current_player(v_session));
    END IF;
    RETURN QUERY SELECT 0, p.total_points FROM players p WHERE p.player_id = v_player;
END;
$$;

-- ===== 5. Завершение партии =====

-- is_session_complete
CREATE FUNCTION is_session_complete(p_session_id integer) RETURNS boolean
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v_amount integer;
BEGIN
    SELECT turn_amount INTO v_amount FROM game_sessions WHERE session_id = p_session_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    RETURN EXISTS (SELECT 1 FROM players WHERE session_id = p_session_id)
       AND NOT EXISTS (
            SELECT 1 FROM players p
            WHERE p.session_id = p_session_id
              AND (SELECT count(1) FROM turns t WHERE t.player_id = p.player_id AND t.is_closed) < v_amount);
END;
$$;

-- get_standings
CREATE FUNCTION get_standings(p_session_id integer)
RETURNS TABLE(player_id integer, user_id integer, login varchar, total_points integer,
              turns_played integer, place integer)
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
BEGIN
    IF NOT EXISTS (SELECT 1 FROM game_sessions s WHERE s.session_id = p_session_id) THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    RETURN QUERY
    SELECT p.player_id, p.user_id, u.login, p.total_points,
           (SELECT count(1) FROM turns t WHERE t.player_id = p.player_id AND t.is_closed)::integer,
           (rank() OVER (ORDER BY p.total_points DESC))::integer
    FROM players p JOIN users u USING (user_id)
    WHERE p.session_id = p_session_id
    ORDER BY 6, p.turn_at;
END;
$$;

-- get_winners
CREATE FUNCTION get_winners(p_session_id integer) RETURNS integer[]
LANGUAGE plpgsql STABLE AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM game_sessions WHERE session_id = p_session_id) THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    RETURN ARRAY(
        SELECT p.player_id FROM players p
        WHERE p.session_id = p_session_id
          AND p.total_points = (SELECT max(total_points) FROM players WHERE session_id = p_session_id)
        ORDER BY p.turn_at);
END;
$$;

-- finish_session
CREATE FUNCTION finish_session(p_session_id integer)
RETURNS TABLE(status text, results jsonb, winner_ids integer[])
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_status session_status;
BEGIN
    SELECT s.status INTO v_status FROM game_sessions s WHERE s.session_id = p_session_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status = 'FINISHED' THEN
        RAISE EXCEPTION 'session_finished' USING ERRCODE = 'P0302';
    ELSIF v_status <> 'IN_PROGRESS' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    IF NOT is_session_complete(p_session_id) THEN
        RAISE EXCEPTION 'session_not_complete' USING ERRCODE = 'P0301';
    END IF;
    UPDATE game_sessions s SET status = 'FINISHED' WHERE s.session_id = p_session_id;
    RETURN QUERY
    SELECT 'FINISHED'::text,
           (SELECT jsonb_agg(to_jsonb(st) ORDER BY st.place, st.player_id) FROM get_standings(p_session_id) st),
           get_winners(p_session_id);
END;
$$;

-- get_session_result
CREATE FUNCTION get_session_result(p_session_id integer)
RETURNS TABLE(status text, results jsonb, winner_ids integer[])
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
DECLARE
    v_status session_status;
BEGIN
    SELECT s.status INTO v_status FROM game_sessions s WHERE s.session_id = p_session_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status <> 'FINISHED' THEN
        RAISE EXCEPTION 'session_not_complete' USING ERRCODE = 'P0301';
    END IF;
    RETURN QUERY
    SELECT 'FINISHED'::text,
           (SELECT jsonb_agg(to_jsonb(st) ORDER BY st.place, st.player_id) FROM get_standings(p_session_id) st),
           get_winners(p_session_id);
END;
$$;

-- ===== Состояние партии =====

-- advance_turn
CREATE FUNCTION advance_turn(p_session_id integer)
RETURNS TABLE(current_player_id integer, turn_number integer, standings jsonb)
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
DECLARE
    v_player integer;
BEGIN
    v_player := current_player(p_session_id);  -- проверяет status = IN_PROGRESS
    RETURN QUERY
    SELECT v_player,
           (SELECT count(1)::integer + 1 FROM turns t WHERE t.player_id = v_player AND t.is_closed),
           (SELECT jsonb_agg(to_jsonb(st) ORDER BY st.place, st.player_id) FROM get_standings(p_session_id) st);
END;
$$;

-- ===== 2. Лобби =====

-- join_lobby
CREATE FUNCTION join_lobby(p_session_id integer, p_user_id integer) RETURNS integer
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
    INSERT INTO players (user_id, session_id, turn_at, total_points)
    SELECT p_user_id, p_session_id, coalesce(max(turn_at), 0) + 1, 0
    FROM players WHERE session_id = p_session_id
    RETURNING player_id INTO v_player_id;
    RETURN v_player_id;
END;
$$;

-- create_lobby
CREATE FUNCTION create_lobby(p_user_id integer, p_turn_amount integer) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_session_id integer;
BEGIN
    IF p_turn_amount IS NULL OR p_turn_amount < 1 THEN
        RAISE EXCEPTION 'invalid_argument' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM users WHERE user_id = p_user_id) THEN
        RAISE EXCEPTION 'user_not_found' USING ERRCODE = 'P0100';
    END IF;
    IF EXISTS (SELECT 1 FROM players p JOIN game_sessions s USING (session_id)
               WHERE p.user_id = p_user_id AND s.status <> 'FINISHED') THEN
        RAISE EXCEPTION 'already_in_session' USING ERRCODE = 'P0107';
    END IF;
    INSERT INTO game_sessions (started_at, status, turn_amount)
    VALUES (now(), 'LOBBY_OPEN', p_turn_amount)
    RETURNING session_id INTO v_session_id;
    PERFORM join_lobby(v_session_id, p_user_id);
    RETURN v_session_id;
END;
$$;

-- list_open_lobbies
CREATE FUNCTION list_open_lobbies(p_limit integer DEFAULT 50, p_offset integer DEFAULT 0)
RETURNS TABLE(session_id integer, turn_amount integer, players_count integer, started_at timestamp)
LANGUAGE sql STABLE AS $$
    SELECT s.session_id, s.turn_amount, count(p.player_id)::integer, s.started_at
    FROM game_sessions s
    LEFT JOIN players p ON p.session_id = s.session_id
    WHERE s.status = 'LOBBY_OPEN'
    GROUP BY s.session_id
    HAVING count(p.player_id) < 4
    ORDER BY s.started_at DESC, s.session_id DESC
    LIMIT p_limit OFFSET p_offset
$$;

-- get_lobby
CREATE FUNCTION get_lobby(p_session_id integer)
RETURNS TABLE(session_id integer, status text, turn_amount integer, players jsonb)
LANGUAGE plpgsql STABLE AS $$
#variable_conflict use_column
BEGIN
    IF NOT EXISTS (SELECT 1 FROM game_sessions s WHERE s.session_id = p_session_id) THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    RETURN QUERY
    SELECT s.session_id, s.status::text, s.turn_amount,
           coalesce((SELECT jsonb_agg(jsonb_build_object(
                         'player_id', p.player_id, 'user_id', p.user_id,
                         'login', u.login, 'turn_at', p.turn_at) ORDER BY p.turn_at)
                     FROM players p JOIN users u USING (user_id)
                     WHERE p.session_id = s.session_id), '[]'::jsonb)
    FROM game_sessions s
    WHERE s.session_id = p_session_id;
END;
$$;

-- leave_lobby
CREATE FUNCTION leave_lobby(p_session_id integer, p_user_id integer) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    v_status session_status;
BEGIN
    SELECT status INTO v_status FROM game_sessions WHERE session_id = p_session_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status <> 'LOBBY_OPEN' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    DELETE FROM players WHERE session_id = p_session_id AND user_id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'not_in_lobby' USING ERRCODE = 'P0106';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM players WHERE session_id = p_session_id) THEN
        DELETE FROM game_sessions WHERE session_id = p_session_id;
    ELSE
        UPDATE players p SET turn_at = r.rn
        FROM (SELECT player_id, row_number() OVER (ORDER BY turn_at)::integer AS rn
              FROM players WHERE session_id = p_session_id) r
        WHERE p.player_id = r.player_id;
    END IF;
END;
$$;

-- assign_turn_at
CREATE FUNCTION assign_turn_at(p_session_id integer) RETURNS void
LANGUAGE sql AS $$
    UPDATE players p SET turn_at = r.rn
    FROM (SELECT player_id, row_number() OVER (ORDER BY random())::integer AS rn
          FROM players WHERE session_id = p_session_id) r
    WHERE p.player_id = r.player_id
$$;

-- start_game
CREATE FUNCTION start_game(p_session_id integer, p_user_id integer)
RETURNS TABLE(session_id integer, current_player_id integer, turn_number integer,
              turn_id integer, throw_number integer, dice jsonb)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_status  session_status;
    v_count   integer;
    v_player  integer;
BEGIN
    SELECT s.status INTO v_status FROM game_sessions s
    WHERE s.session_id = p_session_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status <> 'LOBBY_OPEN' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM players p WHERE p.session_id = p_session_id AND p.user_id = p_user_id) THEN
        RAISE EXCEPTION 'not_in_lobby' USING ERRCODE = 'P0106';
    END IF;
    SELECT count(1) INTO v_count FROM players p WHERE p.session_id = p_session_id;
    IF v_count NOT BETWEEN 2 AND 4 THEN
        RAISE EXCEPTION 'not_enough_players' USING ERRCODE = 'P0105';
    END IF;
    PERFORM assign_turn_at(p_session_id);
    UPDATE game_sessions s SET status = 'IN_PROGRESS', started_at = now()
    WHERE s.session_id = p_session_id;
    v_player := current_player(p_session_id);
    RETURN QUERY
    SELECT p_session_id, v_player, b.turn_number, b.turn_id, b.throw_number, b.dice
    FROM begin_turn(v_player) b;
END;
$$;

-- leave_match
CREATE FUNCTION leave_match(p_session_id integer, p_user_id integer)
RETURNS TABLE(user_id integer, login varchar)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    v_status  session_status;
    v_player  integer;
    v_current integer;
BEGIN
    SELECT s.status INTO v_status FROM game_sessions s WHERE s.session_id = p_session_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'lobby_not_found' USING ERRCODE = 'P0101';
    END IF;
    IF v_status = 'FINISHED' THEN
        RAISE EXCEPTION 'session_finished' USING ERRCODE = 'P0302';
    ELSIF v_status <> 'IN_PROGRESS' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    SELECT p.player_id INTO v_player FROM players p
    WHERE p.session_id = p_session_id AND p.user_id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'not_in_lobby' USING ERRCODE = 'P0106';
    END IF;
    v_current := current_player(p_session_id);
    -- незакрытый ход уходит вместе с игроком (ON DELETE CASCADE), очков за него нет
    DELETE FROM players p WHERE p.player_id = v_player;
    IF NOT EXISTS (SELECT 1 FROM players p WHERE p.session_id = p_session_id) THEN
        DELETE FROM game_sessions s WHERE s.session_id = p_session_id;
    ELSIF is_session_complete(p_session_id) THEN
        PERFORM finish_session(p_session_id);
    ELSIF v_current = v_player THEN
        PERFORM begin_turn(current_player(p_session_id));
    END IF;
    RETURN QUERY SELECT u.user_id, u.login FROM users u WHERE u.user_id = p_user_id;
END;
$$;

-- ===== 6. Служебные функции =====

-- recalc_total_points
CREATE FUNCTION recalc_total_points(p_player_id integer) RETURNS integer
LANGUAGE sql AS $$
    UPDATE players p
    SET total_points = coalesce((SELECT sum(turn_score(t.turn_id))
                                 FROM turns t
                                 WHERE t.player_id = p.player_id AND t.is_closed), 0)
    WHERE p.player_id = p_player_id
    RETURNING p.total_points
$$;

-- seed_combinations
CREATE FUNCTION seed_combinations() RETURNS void
LANGUAGE sql AS $$
    INSERT INTO combinations (name, points) VALUES
        ('Пара', 5), ('Две пары', 10), ('Тройка', 15), ('Малый стрит', 20),
        ('Большой стрит', 25), ('Чёт', 30), ('Нечёт', 30), ('Фул-хаус', 35),
        ('Каре', 40), ('Покер', 50), ('Шанс', 2)
    ON CONFLICT (name) DO UPDATE SET points = EXCLUDED.points
$$;
