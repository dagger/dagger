const { Pool } = require('pg')

describe("Integration Tests", () => {
  test("Database operation", async () => {
    const pool = new Pool({
      user: process.env.DB_USER,
      database: process.env.DB_NAME,
      password: process.env.DB_PASSWORD,
      port: 5432,
      host: process.env.DB_HOST,
    })

    try {
      const res = await pool.query("SELECT 1");
      expect(res.rows[0]["?column?"]).toBe(1)
    } catch (error) {
      expect(error).toBeNull()
    }
    await pool.end()
  });
})