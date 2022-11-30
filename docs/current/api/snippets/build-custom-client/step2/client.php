<?php
// include auto-loader
include 'vendor/autoload.php';

use GraphQL\Client;

try {
  // initialize client with
  // endpoint from environment
  $sessionUrl = getenv('DAGGER_SESSION_URL') or throw new Exception("DAGGER_SESSION_URL doesn't exist");
  $sessionToken = getenv('DAGGER_SESSION_TOKEN') or throw new Exception("DAGGER_SESSION_TOKEN doesn't exist");

  $client = new Client(
    $sessionUrl,
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
