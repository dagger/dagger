package io.dagger.module.info;

import java.util.Map;

public record ModuleInfo(
    String description, ObjectInfo[] objects, Map<String, EnumInfo> enumInfos) {}
