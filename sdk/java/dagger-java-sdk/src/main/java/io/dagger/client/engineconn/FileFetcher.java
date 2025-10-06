package io.dagger.client.engineconn;

import java.io.IOException;
import java.io.InputStream;

@FunctionalInterface
interface FileFetcher {

  InputStream fetch(String url) throws IOException;
}
