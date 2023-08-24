const express = require('express')
const app = express()

console.log('Open a browser to http://localhost:3000')
app.get('/', function (req, res) {
  res.send('Hello from Dagger!')
})

app.listen(3000)
