"""E2E Testing with Dagger - Integration Test Version.

This shows the embedded Dagger pattern adapted for Dagger-in-Dagger execution:
- Services started in module setup
- Service endpoints used directly (no tunnels, since they don't work in Dagger-in-Dagger)
- Environment variables set
- Tests use normal libraries (psycopg2, redis, etc.)
- Tests have NO knowledge of Dagger

To run:
    pip install psycopg2-binary redis  # Install actual database clients
    OTEL_SDK_DISABLED=true pytest test_embedded_dagger_integration.py
"""

import os
import time

import psycopg2
import pytest
import pytest_asyncio
import redis

from dagger.dag import close, dag, init


@pytest_asyncio.fixture(scope="module", autouse=True)
async def setup_services():
    """Set up test services using Dagger directly - runs once for the entire module."""
    # Initialize Dagger
    await init()

    # === PostgreSQL Setup ===
    postgres = (
        dag.container()
        .from_("postgres:16-alpine")
        .with_env_variable("POSTGRES_PASSWORD", "secret")
        .with_env_variable("POSTGRES_USER", "app")
        .with_env_variable("POSTGRES_DB", "app_db")
        .with_exposed_port(5432)
        .as_service()
    )

    # In Dagger-in-Dagger, start the service and get its endpoint directly
    # (no tunnel needed - endpoint will be hostname within Dagger network)
    pg_started = await postgres.start()
    pg_endpoint = await pg_started.endpoint(port=5432)
    pg_host, pg_port = pg_endpoint.split(":")

    # Set environment variables - your app code uses these
    os.environ["DATABASE_URL"] = f"postgresql://app:secret@{pg_host}:{pg_port}/app_db"
    os.environ["PGHOST"] = pg_host
    os.environ["PGPORT"] = pg_port
    os.environ["PGUSER"] = "app"
    os.environ["PGPASSWORD"] = "secret"
    os.environ["PGDATABASE"] = "app_db"

    # Wait for PostgreSQL to be ready
    max_retries = 30
    for i in range(max_retries):
        try:
            conn = psycopg2.connect(os.environ["DATABASE_URL"])
            conn.close()
            print(f"✅ PostgreSQL available at {pg_endpoint}")
            break
        except psycopg2.OperationalError as e:
            if i == max_retries - 1:
                print(f"❌ Failed to connect to PostgreSQL: {e}")
                raise
            time.sleep(1)

    # === Redis Setup ===
    redis_svc = (
        dag.container().from_("redis:7-alpine").with_exposed_port(6379).as_service()
    )

    redis_started = await redis_svc.start()
    redis_endpoint = await redis_started.endpoint(port=6379)
    redis_host, redis_port = redis_endpoint.split(":")

    os.environ["REDIS_URL"] = f"redis://{redis_host}:{redis_port}"
    os.environ["REDIS_HOST"] = redis_host
    os.environ["REDIS_PORT"] = redis_port

    # Wait for Redis to be ready
    for i in range(max_retries):
        try:
            r = redis.from_url(os.environ["REDIS_URL"])
            r.ping()
            r.close()
            print(f"✅ Redis available at {redis_endpoint}")
            break
        except (redis.ConnectionError, redis.TimeoutError) as e:
            if i == max_retries - 1:
                print(f"❌ Failed to connect to Redis: {e}")
                raise
            time.sleep(1)

    yield

    # Teardown
    await close()


class TestDatabaseOperations:
    """Database operation tests."""

    def test_database_connection_available(self):
        """Test PostgreSQL connection is available."""
        conn = psycopg2.connect(os.environ["DATABASE_URL"])
        cursor = conn.cursor()
        cursor.execute("SELECT version()")
        result = cursor.fetchone()
        assert result is not None
        assert "PostgreSQL" in result[0]
        conn.close()

        print("✅ Database connection available")
        print(f"   Connected to: PostgreSQL")

    def test_table_operations(self):
        """Test table operations."""
        conn = psycopg2.connect(os.environ["DATABASE_URL"])
        cursor = conn.cursor()

        # Create table
        cursor.execute("""
            CREATE TABLE IF NOT EXISTS users (
                id SERIAL PRIMARY KEY,
                name TEXT NOT NULL,
                email TEXT UNIQUE NOT NULL
            )
        """)

        # Insert data
        cursor.execute(
            "INSERT INTO users (name, email) VALUES (%s, %s) RETURNING id",
            ("Alice", "alice@example.com"),
        )
        user_id = cursor.fetchone()[0]
        assert user_id is not None

        # Query data
        cursor.execute("SELECT name, email FROM users WHERE id = %s", (user_id,))
        result = cursor.fetchone()
        assert result[0] == "Alice"
        assert result[1] == "alice@example.com"

        conn.commit()
        conn.close()

        print("✅ Table operations working")
        print(f"   Created user with id: {user_id}")


class TestCacheOperations:
    """Cache operation tests."""

    def test_redis_connection_available(self):
        """Test Redis connection is available."""
        r = redis.from_url(os.environ["REDIS_URL"])

        # Test basic operations
        r.set("test:key", "test-value")
        value = r.get("test:key")
        assert value == b"test-value"

        r.close()

        print("✅ Redis connection available")
        print(f"   Connected to: {os.environ['REDIS_URL']}")

    def test_caching_patterns(self):
        """Test caching database query results."""
        import json

        conn = psycopg2.connect(os.environ["DATABASE_URL"])
        cursor = conn.cursor()
        r = redis.from_url(os.environ["REDIS_URL"])

        # Query from database
        cursor.execute("SELECT name, email FROM users WHERE name = %s", ("Alice",))
        result = cursor.fetchone()
        user_data = {"name": result[0], "email": result[1]}

        # Cache the result
        r.setex("user:alice", 3600, json.dumps(user_data))

        # Retrieve from cache
        cached = r.get("user:alice")
        cached_user = json.loads(cached)

        assert cached_user["name"] == "Alice"
        assert cached_user["email"] == "alice@example.com"

        conn.close()
        r.close()

        print("✅ Integration test: DB + Cache working")
        print(f"   Cached user: {cached_user['name']}")


class TestApplicationCode:
    """Application code tests."""

    def test_application_code(self):
        """Demonstrates testing your actual application."""
        # In a real application, you'd test your service layer
        # For this example, we'll simulate a simple user service

        conn = psycopg2.connect(os.environ["DATABASE_URL"])
        cursor = conn.cursor()

        # Simulate creating a user through your app
        cursor.execute(
            "INSERT INTO users (name, email) VALUES (%s, %s) RETURNING id",
            ("Charlie", "charlie@example.com"),
        )
        user_id = cursor.fetchone()[0]

        # Simulate fetching the user
        cursor.execute("SELECT id, name, email FROM users WHERE id = %s", (user_id,))
        user = cursor.fetchone()

        assert user[0] == user_id
        assert user[1] == "Charlie"
        assert user[2] == "charlie@example.com"

        conn.commit()
        conn.close()

        print("✅ Application code testing pattern demonstrated")
        print(f"   Created and verified user: {user[1]}")
