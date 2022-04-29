export default async function DaggerVersionLatestReleased() {
  // get latest released version from 
  const Response = await fetch('https://dl.dagger.io/dagger/latest_version', {})
  console.log(Response);
}
