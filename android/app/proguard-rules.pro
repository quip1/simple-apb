# SPDX-License-Identifier: AGPL-3.0-or-later
# kotlinx.serialization models of the generated API client.
-keepattributes *Annotation*, InnerClasses
-keepclassmembers class org.simpleapb.api.model.** { *; }
-keep,includedescriptorclasses class org.simpleapb.api.model.**$$serializer { *; }
-keepclassmembers class org.simpleapb.api.model.** { *** Companion; }
-keep interface org.simpleapb.api.endpoints.** { *; }
