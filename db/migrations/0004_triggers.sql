-- Сгенерировано из docs/sql-functions.md — при изменении функций правьте документацию и этот файл синхронно.

-- ===== 6. Триггеры =====

-- trg_turns_throw_range
ALTER TABLE turns
    ADD CONSTRAINT turns_throw_range CHECK (throw_number BETWEEN 1 AND 3);

CREATE FUNCTION trg_turns_throw_range_fn() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_amount integer;
BEGIN
    SELECT s.turn_amount INTO v_amount
    FROM players p JOIN game_sessions s USING (session_id)
    WHERE p.player_id = NEW.player_id;
    IF NEW.turn_number NOT BETWEEN 1 AND v_amount THEN
        RAISE EXCEPTION 'invalid_argument: turn_number вне 1..turn_amount' USING ERRCODE = '22023';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_turns_throw_range
BEFORE INSERT OR UPDATE OF turn_number ON turns
FOR EACH ROW EXECUTE FUNCTION trg_turns_throw_range_fn();

-- trg_turns_after_combo
CREATE FUNCTION trg_turns_after_combo_fn() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_session integer;
BEGIN
    UPDATE players SET total_points = total_points + turn_score(NEW.turn_id)
    WHERE player_id = NEW.player_id;
    SELECT session_id INTO v_session FROM players WHERE player_id = NEW.player_id;
    IF is_session_complete(v_session) THEN
        PERFORM finish_session(v_session);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER trg_turns_after_combo
AFTER UPDATE OF is_closed ON turns
FOR EACH ROW WHEN (NEW.is_closed AND NOT OLD.is_closed)
EXECUTE FUNCTION trg_turns_after_combo_fn();

-- trg_players_limit
CREATE FUNCTION trg_players_limit_fn() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_status session_status;
BEGIN
    SELECT status INTO v_status FROM game_sessions WHERE session_id = NEW.session_id FOR SHARE;
    IF v_status <> 'LOBBY_OPEN' THEN
        RAISE EXCEPTION 'lobby_not_open' USING ERRCODE = 'P0102';
    END IF;
    IF (SELECT count(1) FROM players WHERE session_id = NEW.session_id) >= 4 THEN
        RAISE EXCEPTION 'lobby_full' USING ERRCODE = 'P0103';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_players_limit
BEFORE INSERT ON players
FOR EACH ROW EXECUTE FUNCTION trg_players_limit_fn();

-- trg_dice_count
CREATE FUNCTION trg_dice_count_fn() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM turns WHERE turn_id = NEW.turn_id)
       AND (SELECT count(1) FROM dice WHERE turn_id = NEW.turn_id) <> 5 THEN
        RAISE EXCEPTION 'invalid_argument: у хода должно быть ровно 5 костей' USING ERRCODE = '22023';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_dice_count
AFTER INSERT ON dice
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_dice_count_fn();

-- trg_sessions_status
CREATE FUNCTION trg_sessions_status_fn() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    IF (OLD.status, NEW.status) NOT IN (('LOBBY_OPEN', 'IN_PROGRESS'), ('IN_PROGRESS', 'FINISHED')) THEN
        RAISE EXCEPTION 'недопустимый переход статуса: % -> %', OLD.status, NEW.status
            USING ERRCODE = 'P0102';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_sessions_status
BEFORE UPDATE OF status ON game_sessions
FOR EACH ROW EXECUTE FUNCTION trg_sessions_status_fn();
