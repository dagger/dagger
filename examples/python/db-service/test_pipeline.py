import psycopg2
import os

def test_db():
    conn = psycopg2.connect(database = os.environ["DB_NAME"], user = os.environ["DB_USER"], password = os.environ["DB_PASSWORD"], host = os.environ["DB_HOST"], port = "5432")
    cur = conn.cursor()
    cur.execute("SELECT 1")
    rows = cur.fetchall()
    for row in rows:
      assert row[0] == 1
    conn.close()