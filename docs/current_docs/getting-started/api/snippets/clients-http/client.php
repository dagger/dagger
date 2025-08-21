<?php
// include auto-loader
include 'vendor/autoload.php';

use GraphQL\Client;

try {
  // initialize client with
  // endpoint from environment
  $sessionPort = getenv('DAGGER_SESSION_PORT') or throw new Exception("DAGGER_SESSION_PORT doesn't exist");
  $sessionToken = getenv('DAGGER_SESSION_TOKEN') or throw new Exception("DAGGER_SESSION_TOKEN doesn't exist");

  $client = new Client(
    'http://127.0.0.1:' . $sessionPort . '/query',
    ['Authorization' => 'Basic ' . base64_encode($sessionToken . ':')]
  );

  // define raw GraphQL query
  $query = <<<QUERY
  query {
    container {
      from (address: "alpine:latest") {
        withExec(args:["uname", "-nrio"]) {
          stdout
        }
      }
    }
  }
  QUERY;

  // execute query and print result
  $results = $client->runRawQuery($query);
  print_r($results->getData()->container->from->withExec->stdout);
} catch (Exception $e) {
  print_r($e->getMessage());
  exit;
}
