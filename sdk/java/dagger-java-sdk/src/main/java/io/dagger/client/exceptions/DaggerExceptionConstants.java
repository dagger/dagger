package io.dagger.client.exceptions;

public class DaggerExceptionConstants {
        public static final String CMD_KEY = "cmd";
        public static final String EXIT_CODE_KEY = "exitCode";
        public static final String STDERR_KEY = "stderr";
        public static final String TYPE_KEY = "_type";

        public static final String TYPE_EXEC_ERROR_VALUE = "EXEC_ERROR";

        protected static final String SIMPLE_MESSAGE =
                        "Message: [%s]\nPath: [%s]\nType Code: [%s]\n";
        protected static final String ENHANCED_MESSAGE =
                        "Message: [%s]\nPath: [%s]\nType Code: [%s]\nExit Code: [%s]\nCmd: [%s]\n";
        protected static final String FULL_MESSAGE =
                        "Message: [%s]\nPath: [%s]\nType Code: [%s]\nExit Code: [%s]\nCmd: [%s]\nSTDERR: [%s]\n";

        private DaggerExceptionConstants() {

        }
}
