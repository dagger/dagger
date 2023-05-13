using DaggerSDK.GraphQL.QueryElements;

namespace IntegrationTests.TestData;

// taken from: 
public class LaravelExample
{
    public static string RuntimeQuery = """
        query {
            container(platform: "linux/amd64") {
                from(address: "php:8.2-apache-buster") {
                    withExec(args: ["apt-get","update"]) {
                        withExec(args: ["apt-get","install","--yes","git-core"]) {
                            withExec(args: ["apt-get","install","--yes","zip"]) {
                                withExec(args: ["apt-get","install","--yes","curl"]) {
                                    withExec(args: ["docker-php-ext-install","pdo","pdo_mysql","mysqli"]) {
                                        withExec(args: ["sh","-c","sed -ri -e 's!/var/www/html!/var/www/public!g' /etc/apache2/sites-available/*.conf"]) {
                                            withExec(args: ["sh","-c","sed -ri -e 's!/var/www/!/var/www/public!g' /etc/apache2/apache2.conf /etc/apache2/conf-available/*.conf"]) {
                                                withExec(args: ["a2enmod","rewrite"]) {
                                                    stdout,
                                                    stderr
                                                }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
        """;

    public static GraphQLElement RuntimeQueryElement = new Container(platform: "linux/amd64", new[] {
        new From(address: "php:8.2-apache-buster", new[]
        {
            new WithExec(args: new ("apt-get", "update"), new []
            {
                new WithExec(args: new ("apt-get", "install", "--yes", "git-core"), new []
                {
                    new WithExec(args: new ("apt-get", "install", "--yes", "zip"), new[]
                    {
                        new WithExec(args: new ("apt-get", "install", "--yes", "curl"), new[]
                        {
                            new WithExec(args: new ("docker-php-ext-install", "pdo", "pdo_mysql", "mysqli"), new[]
                            {
                                new WithExec(args: new ("sh", "-c", "sed -ri -e 's!/var/www/html!/var/www/public!g' /etc/apache2/sites-available/*.conf"), new[]
                                {
                                    new WithExec(args: new ("sh", "-c", "sed -ri -e 's!/var/www/!/var/www/public!g' /etc/apache2/apache2.conf /etc/apache2/conf-available/*.conf"), new[]
                                    {
                                        new WithExec(args: new ("a2enmod", "rewrite"), new []
                                        {
                                            (GraphQLElement) "stdout",
                                            (GraphQLElement) "stderr"
                                        })
                                    })
                                })
                            })
                        })
                    })
                })
            })
        })
    });
}
