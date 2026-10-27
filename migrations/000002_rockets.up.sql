CREATE TABLE rockets (
    channel text PRIMARY KEY CHECK (btrim(channel) <> ''),
    rocket_type text NOT NULL DEFAULT '',
    speed bigint NOT NULL DEFAULT 0,
    mission text NOT NULL DEFAULT '',
    exploded boolean NOT NULL DEFAULT false,
    explosion_reason text NOT NULL DEFAULT '',
    last_applied_message_number bigint NOT NULL DEFAULT 0 CHECK (last_applied_message_number >= 0),
    last_applied_message_time text,
    CHECK ((last_applied_message_number = 0) = (last_applied_message_time IS NULL))
);

CREATE TABLE rocket_events (
    channel text NOT NULL REFERENCES rockets(channel),
    message_number bigint NOT NULL CHECK (message_number > 0),
    message_type text NOT NULL CHECK (message_type IN (
        'RocketLaunched', 'RocketSpeedIncreased', 'RocketSpeedDecreased',
        'RocketExploded', 'RocketMissionChanged'
    )),
    message_time timestamptz NOT NULL,
    -- PostgreSQL timestamps have microsecond precision. Preserve the exact
    -- instant separately to retain nanosecond event history.
    message_time_exact text NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    received_at timestamptz NOT NULL,
    PRIMARY KEY (channel, message_number)
);
