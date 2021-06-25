import express from "express";
import { get } from "env-var";


const app = express();

const port: number = get('PORT').required().asPortNumber();

app.get('/ping', (req, res) => {
  res.status(200).send('pong')
});

app.listen(port, '0.0.0.0', () => console.log("Server listen on http://localhost:" + port));