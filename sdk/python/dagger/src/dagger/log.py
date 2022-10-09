import logging
import logging.config


def configure_logging(level: int | str = logging.INFO):
    config = {
        "version": 1,
        "disable_existing_loggers": False,
        "formatters": {
            "simple": {"format": "[{levelname}] {name}: {message}", "style": "{"},
        },
        "handlers": {
            "console": {
                "level": "DEBUG",
                "class": "logging.StreamHandler",
                "formatter": "simple",
            },
        },
        "loggers": {
            "dagger": {
                "handlers": ["console"],
                "level": level,
            },
        },
    }
    logging.config.dictConfig(config)
