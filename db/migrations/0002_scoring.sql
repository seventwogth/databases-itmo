-- Сгенерировано из docs/sql-functions.md — при изменении функций правьте документацию и этот файл синхронно.

-- ===== Комбинации и подсчёт очков (IMMUTABLE) =====

-- dice_counts
CREATE FUNCTION dice_counts(p_dice integer[]) RETURNS integer[]
LANGUAGE sql IMMUTABLE AS $$
    SELECT array_agg(cnt ORDER BY v)
    FROM (SELECT g AS v, count(d)::integer AS cnt
          FROM generate_series(1, 6) AS g
          LEFT JOIN unnest(p_dice) AS d ON d = g
          GROUP BY g) t
$$;

-- has_pair
CREATE FUNCTION has_pair(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT EXISTS (SELECT 1 FROM unnest(dice_counts(p_dice)) AS c WHERE c >= 2)
$$;

-- has_two_pairs
CREATE FUNCTION has_two_pairs(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT (SELECT count(1) FROM unnest(dice_counts(p_dice)) AS c WHERE c >= 2) >= 2
$$;

-- has_three_of_a_kind
CREATE FUNCTION has_three_of_a_kind(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT EXISTS (SELECT 1 FROM unnest(dice_counts(p_dice)) AS c WHERE c >= 3)
$$;

-- has_small_straight
CREATE FUNCTION has_small_straight(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT (c[1] > 0 AND c[2] > 0 AND c[3] > 0 AND c[4] > 0)
        OR (c[2] > 0 AND c[3] > 0 AND c[4] > 0 AND c[5] > 0)
        OR (c[3] > 0 AND c[4] > 0 AND c[5] > 0 AND c[6] > 0)
    FROM (SELECT dice_counts(p_dice) AS c) t
$$;

-- has_large_straight
CREATE FUNCTION has_large_straight(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT (c[1] > 0 AND c[2] > 0 AND c[3] > 0 AND c[4] > 0 AND c[5] > 0)
        OR (c[2] > 0 AND c[3] > 0 AND c[4] > 0 AND c[5] > 0 AND c[6] > 0)
    FROM (SELECT dice_counts(p_dice) AS c) t
$$;

-- has_even
CREATE FUNCTION has_even(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT cardinality(p_dice) = 5
       AND (SELECT bool_and(d % 2 = 0) FROM unnest(p_dice) AS d)
$$;

-- has_odd
CREATE FUNCTION has_odd(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT cardinality(p_dice) = 5
       AND (SELECT bool_and(d % 2 = 1) FROM unnest(p_dice) AS d)
$$;

-- has_full_house
CREATE FUNCTION has_full_house(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT count(1) FILTER (WHERE c = 3) = 1
       AND count(1) FILTER (WHERE c = 2) = 1
    FROM unnest(dice_counts(p_dice)) AS c
$$;

-- has_four_of_a_kind
CREATE FUNCTION has_four_of_a_kind(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT EXISTS (SELECT 1 FROM unnest(dice_counts(p_dice)) AS c WHERE c >= 4)
$$;

-- has_poker
CREATE FUNCTION has_poker(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT EXISTS (SELECT 1 FROM unnest(dice_counts(p_dice)) AS c WHERE c = 5)
$$;

-- has_chance
CREATE FUNCTION has_chance(p_dice integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT cardinality(p_dice) = 5
       AND (SELECT count(DISTINCT d) = 5 FROM unnest(p_dice) AS d)
$$;

-- combo_matches
CREATE FUNCTION combo_matches(p_dice integer[], p_combo_name varchar) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE p_combo_name
        WHEN 'Пара'          THEN has_pair(p_dice)
        WHEN 'Две пары'      THEN has_two_pairs(p_dice)
        WHEN 'Тройка'        THEN has_three_of_a_kind(p_dice)
        WHEN 'Малый стрит'   THEN has_small_straight(p_dice)
        WHEN 'Большой стрит' THEN has_large_straight(p_dice)
        WHEN 'Чёт'           THEN has_even(p_dice)
        WHEN 'Нечёт'         THEN has_odd(p_dice)
        WHEN 'Фул-хаус'      THEN has_full_house(p_dice)
        WHEN 'Каре'          THEN has_four_of_a_kind(p_dice)
        WHEN 'Покер'         THEN has_poker(p_dice)
        WHEN 'Шанс'          THEN has_chance(p_dice)
        ELSE FALSE
    END
$$;

-- combo_dice_sum
CREATE FUNCTION combo_dice_sum(p_dice integer[], p_combo_name varchar) RETURNS integer
LANGUAGE sql IMMUTABLE AS $$
    WITH h AS (
        SELECT v::integer AS v, c FROM unnest(dice_counts(p_dice)) WITH ORDINALITY AS t(c, v)
    )
    SELECT CASE
        WHEN NOT combo_matches(p_dice, p_combo_name) THEN 0
        ELSE CASE p_combo_name
            WHEN 'Пара'        THEN (SELECT 2 * max(v) FROM h WHERE c >= 2)
            WHEN 'Две пары'    THEN (SELECT 2 * sum(v) FROM h WHERE c >= 2)
            WHEN 'Тройка'      THEN (SELECT 3 * max(v) FROM h WHERE c >= 3)
            WHEN 'Каре'        THEN (SELECT 4 * max(v) FROM h WHERE c >= 4)
            WHEN 'Малый стрит' THEN (
                SELECT max(4 * s + 6) FROM generate_series(1, 3) AS s
                WHERE (SELECT count(1) FROM h WHERE v BETWEEN s AND s + 3 AND c > 0) = 4)
            ELSE (SELECT sum(d) FROM unnest(p_dice) AS d)
        END
    END::integer
$$;

-- calc_score
CREATE FUNCTION calc_score(p_dice integer[], p_combo_name varchar,
                           p_combo_points integer, p_throw_number integer) RETURNS integer
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN NOT combo_matches(p_dice, p_combo_name) THEN 0
        ELSE (combo_dice_sum(p_dice, p_combo_name) + p_combo_points)
             * CASE WHEN p_throw_number = 1 AND p_combo_name <> 'Шанс' THEN 2 ELSE 1 END
    END
$$;
