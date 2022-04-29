export default function DaggerVersionLatestReleased() {
  // get latest released version from 
  const Response = fetch('https://dl.dagger.io/dagger/latest_version', {}).then(response => {
    console.log(response);
    response.body;
  }).catch(error => {
    console.error(error);
    '0.2.8';
  });
  return Response;
}
